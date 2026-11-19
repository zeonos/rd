// rd-api is the RD API server and its admin command line.
//
//	rd-api [serve]                     run the server (default)
//	rd-api totp-setup <user>           set up the authenticator app for a user
//	rd-api set-password <user>         set a new password (prompts)
//	rd-api create-user <user> [--admin]
//	rd-api token create [--group "Office/Rack 1"] [--max-uses N] [--expires-hours H] [--note text]
//	rd-api token list | token delete <id>
//	rd-api group list | group add "<path>" | group delete <id>
//	rd-api job-key
//	rd-api device list | device move <id> "<group path>" | device rename <id> <alias> | device delete <id>
//
// Configuration comes from environment variables (see deploy/docker-compose.yml).
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
	_ "time/tzdata" // schedules use IANA time zones; the image has no zoneinfo

	"github.com/zeonos/rd/server/internal/api"
	"github.com/zeonos/rd/server/internal/auth"
	"github.com/zeonos/rd/server/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Println(usage)
		return
	}
	st, err := store.OpenPostgres(ctx, env("RD_DATABASE_URL", "postgres://rd:rd@localhost:5432/rd?sslmode=disable"))
	if err != nil {
		fatal(err)
	}
	defer st.Close()

	switch cmd {
	case "serve":
		err = serve(ctx, st)
	case "totp-setup":
		err = totpSetup(ctx, st, args)
	case "set-password":
		err = setPassword(ctx, st, args)
	case "create-user":
		err = createUser(ctx, st, args)
	case "token":
		err = tokenCmd(ctx, st, args)
	case "group":
		err = groupCmd(ctx, st, args)
	case "device":
		err = deviceCmd(ctx, st, args)
	case "job-key":
		err = jobKeyCmd(ctx, st)
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	if err != nil {
		fatal(err)
	}
}

const usage = `rd-api commands:
  serve                                  run the API server (default)
  totp-setup <user>                      set up the authenticator app (required before login)
  set-password <user>                    set a new password
  create-user <user> [--admin]           add a user
  token create [--group PATH] [--max-uses N] [--expires-hours H] [--note TEXT]
  token list | token delete <id>
  group list | group add PATH | group delete <id>
  device list | device move <id> PATH | device rename <id> ALIAS | device delete <id>
  job-key                                print the public job signing key (goes into the client build)`

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// --- serve ---

func serve(ctx context.Context, st store.Store) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := bootstrapAdmin(ctx, st, logger); err != nil {
		return err
	}
	ntfy := os.Getenv("RD_ALERT_NTFY_URL")
	srv := api.New(st, api.Config{
		RequireTOTP:      env("RD_REQUIRE_TOTP", "1") != "0",
		OfflineAlert:     time.Duration(envInt("RD_ALERT_OFFLINE_MINUTES", 10)) * time.Minute,
		TrustProxy:       env("RD_TRUSTED_PROXY", "0") == "1",
		DataDir:          env("RD_DATA_DIR", "/data"),
		DiskAlertPercent: envInt("RD_ALERT_DISK_PERCENT", 10),
		SyncAlert:        time.Duration(envInt("RD_ALERT_SYNC_MINUTES", 240)) * time.Minute,
		Logger:           logger,
		Notify: func(title, msg string) {
			logger.Info("notify", "title", title, "message", msg)
			if ntfy == "" {
				return
			}
			req, err := http.NewRequest("POST", ntfy, bytes.NewBufferString(msg))
			if err != nil {
				return
			}
			req.Header.Set("Title", title)
			req.Header.Set("Tags", "lock")
			c := &http.Client{Timeout: 10 * time.Second}
			if resp, err := c.Do(req); err != nil {
				logger.Warn("ntfy", "err", err)
			} else {
				resp.Body.Close()
			}
		},
	})
	go srv.Maintain(ctx)
	hs := &http.Server{
		Addr:              env("RD_LISTEN", ":8080"),
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	logger.Info("listening", "addr", hs.Addr)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// bootstrapAdmin creates the first admin from RD_ADMIN_USER / RD_ADMIN_PASSWORD
// when the database has no users. Without a password, a random one is logged once.
func bootstrapAdmin(ctx context.Context, st store.Store, logger *slog.Logger) error {
	n, err := st.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	name := env("RD_ADMIN_USER", "admin")
	pw := os.Getenv("RD_ADMIN_PASSWORD")
	generated := pw == ""
	if generated {
		pw = auth.RandomPassword(20)
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := st.CreateUser(ctx, &store.User{Name: name, PasswordHash: hash, IsAdmin: true}); err != nil {
		return err
	}
	if generated {
		logger.Warn("created first admin - note this password now, it is shown only once",
			"user", name, "admin_password", pw)
	} else {
		logger.Info("created first admin from RD_ADMIN_PASSWORD", "user", name)
	}
	logger.Warn("next step: docker compose exec -it api rd-api totp-setup " + name)
	return nil
}

// --- user commands ---

var stdin = bufio.NewReader(os.Stdin)

func prompt(label string) string {
	fmt.Print(label)
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line)
}

func totpSetup(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: rd-api totp-setup <user>")
	}
	u, err := st.GetUserByName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("user %q: %w", args[0], err)
	}
	secret := auth.NewTOTPSecret()
	issuer := "RD " + strings.TrimPrefix(strings.TrimPrefix(env("RD_PUBLIC_URL", "rd"), "https://"), "http://")
	fmt.Println("Add this account to your authenticator app (Microsoft/Google Authenticator, 1Password, ...):")
	fmt.Println()
	fmt.Println("  Setup key (manual entry):", secret)
	fmt.Println("  Account:", u.Name, "   Type: time-based")
	fmt.Println()
	fmt.Println("  Or open this link on the phone / paste it into a QR generator:")
	fmt.Println(" ", auth.TOTPURL(issuer, u.Name, secret))
	fmt.Println()
	for attempt := 0; attempt < 3; attempt++ {
		code := prompt("Enter the 6-digit code from the app to confirm: ")
		if _, ok := auth.VerifyTOTP(secret, code, time.Now()); ok {
			if err := st.SetUserTOTP(ctx, u.ID, secret, true); err != nil {
				return err
			}
			if err := st.DeleteUserSessions(ctx, u.ID); err != nil {
				return err
			}
			fmt.Println("TOTP enabled. Existing sessions were signed out.")
			return nil
		}
		fmt.Println("That code is not valid, try again (check the phone's clock).")
	}
	return errors.New("TOTP not enabled")
}

func readNewPassword() (string, error) {
	pw := prompt("New password (min 12 characters): ")
	if len(pw) < 12 {
		return "", errors.New("password too short")
	}
	if prompt("Repeat: ") != pw {
		return "", errors.New("passwords do not match")
	}
	return pw, nil
}

func setPassword(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: rd-api set-password <user>")
	}
	u, err := st.GetUserByName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("user %q: %w", args[0], err)
	}
	pw, err := readNewPassword()
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := st.SetUserPassword(ctx, u.ID, hash); err != nil {
		return err
	}
	fmt.Println("Password changed. Existing sessions were signed out.")
	return st.DeleteUserSessions(ctx, u.ID)
}

func createUser(ctx context.Context, st store.Store, args []string) error {
	fs := flag.NewFlagSet("create-user", flag.ContinueOnError)
	admin := fs.Bool("admin", false, "make the user an admin")
	if len(args) < 1 {
		return errors.New("usage: rd-api create-user <user> [--admin]")
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	pw, err := readNewPassword()
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := st.CreateUser(ctx, &store.User{Name: name, PasswordHash: hash, IsAdmin: *admin}); err != nil {
		return err
	}
	fmt.Printf("User %s created. Next: rd-api totp-setup %s\n", name, name)
	return nil
}

// --- tokens, groups, devices ---

func table() *tabwriter.Writer { return tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0) }

func tokenCmd(ctx context.Context, st store.Store, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rd-api token create|list|delete")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("token create", flag.ContinueOnError)
		group := fs.String("group", "", "location for devices enrolled with this token (empty: device chooses)")
		maxUses := fs.Int("max-uses", 0, "maximum number of devices (0 = unlimited)")
		expires := fs.Int("expires-hours", 0, "expiry in hours (0 = never)")
		note := fs.String("note", "", "free text")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		t, err := api.CreateToken(ctx, st, time.Now(), api.CreateTokenRequest{Group: *group, Note: *note, MaxUses: *maxUses, ExpiresInH: *expires})
		if err != nil {
			return err
		}
		fmt.Println("Enrolment token (shown once):")
		fmt.Println()
		fmt.Println("  " + t.Token)
		fmt.Println()
		if t.Group != "" {
			fmt.Println("Devices enrolled with it go to:", t.Group)
		} else {
			fmt.Println("Devices choose their location with GROUP=... in the installer.")
		}
		return nil
	case "list":
		tokens, err := st.ListEnrollTokens(ctx)
		if err != nil {
			return err
		}
		w := table()
		fmt.Fprintln(w, "ID\tGROUP\tUSES\tEXPIRES\tNOTE")
		for _, t := range tokens {
			uses := strconv.Itoa(t.Uses)
			if t.MaxUses > 0 {
				uses += "/" + strconv.Itoa(t.MaxUses)
			}
			exp := "never"
			if t.ExpiresAt != nil {
				exp = t.ExpiresAt.Local().Format("2006-01-02 15:04")
			}
			g := t.GroupPath
			if g == "" {
				g = "(device chooses)"
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", t.ID, g, uses, exp, t.Note)
		}
		return w.Flush()
	case "delete":
		if len(args) != 2 {
			return errors.New("usage: rd-api token delete <id>")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return err
		}
		return st.DeleteEnrollToken(ctx, id)
	}
	return fmt.Errorf("unknown token command %q", args[0])
}

func groupCmd(ctx context.Context, st store.Store, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rd-api group list|add|delete")
	}
	switch args[0] {
	case "list":
		groups, err := st.ListGroups(ctx)
		if err != nil {
			return err
		}
		w := table()
		fmt.Fprintln(w, "ID\tPATH")
		for _, g := range groups {
			fmt.Fprintf(w, "%d\t%s\n", g.ID, g.Path)
		}
		return w.Flush()
	case "add":
		if len(args) != 2 {
			return errors.New(`usage: rd-api group add "Office/Rack 1"`)
		}
		g, err := st.EnsureGroupPath(ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Group %d: %s\n", g.ID, g.Path)
		return nil
	case "delete":
		if len(args) != 2 {
			return errors.New("usage: rd-api group delete <id>")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return err
		}
		if err := st.DeleteGroup(ctx, id); errors.Is(err, store.ErrConflict) {
			return errors.New("group still has devices, subgroups or enrolment tokens")
		} else if err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("unknown group command %q", args[0])
}

func deviceCmd(ctx context.Context, st store.Store, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rd-api device list|move|rename|delete")
	}
	switch args[0] {
	case "list":
		devices, err := st.ListDevices(ctx)
		if err != nil {
			return err
		}
		now := time.Now()
		w := table()
		fmt.Fprintln(w, "ID\tGROUP\tNAME\tOS\tSTATUS\tUNATTENDED\tLAST SEEN")
		for i := range devices {
			d := &devices[i]
			name := d.Hostname
			if d.Alias != "" {
				name = d.Alias + " (" + d.Hostname + ")"
			}
			status := "offline"
			if d.Online(now) {
				status = "online"
			}
			seen := "never"
			if d.LastSeen != nil {
				seen = d.LastSeen.Local().Format("2006-01-02 15:04")
			}
			group := d.GroupPath
			if group == "" {
				group = "Unassigned"
			}
			un := "no"
			if d.PasswordH1 != "" {
				un = "yes"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", d.ID, group, name, d.OS, status, un, seen)
		}
		return w.Flush()
	case "move":
		if len(args) != 3 {
			return errors.New(`usage: rd-api device move <id> "Office/Rack 1"   ("" = unassigned)`)
		}
		var gid *int64
		if strings.TrimSpace(args[2]) != "" {
			g, err := st.EnsureGroupPath(ctx, args[2])
			if err != nil {
				return err
			}
			gid = &g.ID
		}
		return st.UpdateDevice(ctx, args[1], nil, nil, &gid)
	case "rename":
		if len(args) != 3 {
			return errors.New("usage: rd-api device rename <id> <alias>")
		}
		return st.UpdateDevice(ctx, args[1], &args[2], nil, nil)
	case "delete":
		if len(args) != 2 {
			return errors.New("usage: rd-api device delete <id>")
		}
		return st.DeleteDevice(ctx, args[1])
	}
	return fmt.Errorf("unknown device command %q", args[0])
}

func jobKeyCmd(ctx context.Context, st store.Store) error {
	k, err := api.JobKey(ctx, st)
	if err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey)))
	return nil
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
