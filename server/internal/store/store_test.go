package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// Every Store implementation must pass these tests. PostgreSQL runs when
// RD_TEST_DATABASE_URL points at an empty database (CI does this).
func stores(t *testing.T) map[string]Store {
	t.Helper()
	out := map[string]Store{"memory": NewMemory()}
	if url := os.Getenv("RD_TEST_DATABASE_URL"); url != "" {
		ctx := context.Background()
		p, err := OpenPostgres(ctx, url)
		if err != nil {
			t.Fatalf("postgres: %v", err)
		}
		for _, tbl := range []string{"sessions", "devices", "enroll_tokens", "device_groups", "users"} {
			if _, err := p.pool.Exec(ctx, "DELETE FROM "+tbl); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(p.Close)
		out["postgres"] = p
	}
	return out
}

func TestStore(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)

			// users
			u := &User{Name: "admin", PasswordHash: "h", IsAdmin: true}
			if err := s.CreateUser(ctx, u); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateUser(ctx, &User{Name: "ADMIN", PasswordHash: "x"}); !errors.Is(err, ErrConflict) {
				t.Fatalf("duplicate user: %v", err)
			}
			got, err := s.GetUserByName(ctx, "Admin")
			if err != nil || got.ID != u.ID || !got.IsAdmin {
				t.Fatalf("get user: %v %+v", err, got)
			}
			if err := s.SetUserTOTP(ctx, u.ID, "SECRET", true); err != nil {
				t.Fatal(err)
			}
			if ok, _ := s.AdvanceTOTPStep(ctx, u.ID, 10); !ok {
				t.Fatal("first step should be accepted")
			}
			if ok, _ := s.AdvanceTOTPStep(ctx, u.ID, 10); ok {
				t.Fatal("replayed step must be rejected")
			}

			// sessions
			sess := &Session{TokenHash: "th", UserID: u.ID, Kind: "client", CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(time.Hour)}
			if err := s.CreateSession(ctx, sess); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetSession(ctx, "th"); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteExpiredSessions(ctx, now.Add(2*time.Hour), 24*time.Hour); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetSession(ctx, "th"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("expired session still there: %v", err)
			}

			// groups
			rack, err := s.EnsureGroupPath(ctx, " Office / Rack 1 ")
			if err != nil || rack.Path != "Office/Rack 1" {
				t.Fatalf("ensure path: %v %+v", err, rack)
			}
			again, err := s.EnsureGroupPath(ctx, "office/rack 1")
			if err != nil || again.ID != rack.ID {
				t.Fatalf("ensure path is not idempotent: %v %+v", err, again)
			}
			office, err := s.FindGroupPath(ctx, "Office")
			if err != nil || rack.ParentID == nil || *rack.ParentID != office.ID {
				t.Fatalf("parent: %v", err)
			}
			if _, err := s.FindGroupPath(ctx, "Nowhere"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("find missing: %v", err)
			}
			if err := s.DeleteGroup(ctx, office.ID); !errors.Is(err, ErrConflict) {
				t.Fatalf("delete parent with child: %v", err)
			}

			// tokens
			exp := now.Add(time.Hour)
			tok := &EnrollToken{TokenHash: "tok", GroupID: &rack.ID, MaxUses: 1, ExpiresAt: &exp}
			if err := s.CreateEnrollToken(ctx, tok); err != nil {
				t.Fatal(err)
			}
			used, err := s.UseEnrollToken(ctx, "tok", now)
			if err != nil || used.GroupPath != "Office/Rack 1" || used.Uses != 1 {
				t.Fatalf("use token: %v %+v", err, used)
			}
			if _, err := s.UseEnrollToken(ctx, "tok", now); !errors.Is(err, ErrTokenUnusable) {
				t.Fatalf("token reuse: %v", err)
			}
			if _, err := s.UseEnrollToken(ctx, "nope", now); !errors.Is(err, ErrNotFound) {
				t.Fatalf("unknown token: %v", err)
			}

			// devices
			d := &Device{ID: "123456789", UUID: "u1", GroupID: &rack.ID}
			if err := s.EnrollDevice(ctx, d); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateSysinfo(ctx, d.ID, SysinfoUpdate{Hostname: "SRV1", OS: "Windows Server 2022", PasswordH1: "h1"}); err != nil {
				t.Fatal(err)
			}
			if err := s.Heartbeat(ctx, d.ID, 2, now); err != nil {
				t.Fatal(err)
			}
			gd, err := s.GetDevice(ctx, d.ID)
			if err != nil || gd.Hostname != "SRV1" || gd.PasswordH1 != "h1" || gd.GroupPath != "Office/Rack 1" || gd.Conns != 2 || !gd.Online(now) {
				t.Fatalf("device: %v %+v", err, gd)
			}
			// sysinfo without h1 keeps the stored one
			if err := s.UpdateSysinfo(ctx, d.ID, SysinfoUpdate{Hostname: "SRV1"}); err != nil {
				t.Fatal(err)
			}
			if gd, _ = s.GetDevice(ctx, d.ID); gd.PasswordH1 != "h1" {
				t.Fatal("h1 lost on sysinfo without h1")
			}
			// same uuid re-enrol keeps h1, new uuid clears it
			if err := s.EnrollDevice(ctx, &Device{ID: d.ID, UUID: "u1", GroupID: &office.ID}); err != nil {
				t.Fatal(err)
			}
			if gd, _ = s.GetDevice(ctx, d.ID); gd.PasswordH1 != "h1" || gd.GroupPath != "Office" {
				t.Fatalf("re-enrol same uuid: %+v", gd)
			}
			if err := s.EnrollDevice(ctx, &Device{ID: d.ID, UUID: "u2", GroupID: &office.ID}); err != nil {
				t.Fatal(err)
			}
			if gd, _ = s.GetDevice(ctx, d.ID); gd.PasswordH1 != "" || gd.UUID != "u2" {
				t.Fatalf("re-enrol new uuid: %+v", gd)
			}
			alias := "Web server"
			if err := s.UpdateDevice(ctx, d.ID, &alias, nil, nil); err != nil {
				t.Fatal(err)
			}
			var none *int64
			if err := s.UpdateDevice(ctx, d.ID, nil, nil, &none); err != nil {
				t.Fatal(err)
			}
			if gd, _ = s.GetDevice(ctx, d.ID); gd.Alias != alias || gd.GroupID != nil {
				t.Fatalf("update device: %+v", gd)
			}
			if err := s.Heartbeat(ctx, "unknown", 0, now); !errors.Is(err, ErrNotFound) {
				t.Fatalf("heartbeat unknown: %v", err)
			}
			list, err := s.ListDevices(ctx)
			if err != nil || len(list) != 1 {
				t.Fatalf("list devices: %v %d", err, len(list))
			}
			if err := s.DeleteDevice(ctx, d.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteEnrollToken(ctx, tok.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteGroup(ctx, rack.ID); err != nil {
				t.Fatalf("delete empty group: %v", err)
			}
		})
	}
}
