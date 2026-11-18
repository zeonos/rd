package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zeonos/rd/server/internal/auth"
	"github.com/zeonos/rd/server/internal/store"
)

// loginRequest is what the RustDesk client posts to /api/login.
// Step 1: type "account" with username + password.
// Step 2 (TOTP): type "email_code" with username, tfaCode and the secret from step 1.
type loginRequest struct {
	Username         string `json:"username"`
	Password         string `json:"password"`
	ID               string `json:"id"`
	UUID             string `json:"uuid"`
	Type             string `json:"type"`
	TfaCode          string `json:"tfaCode"`
	VerificationCode string `json:"verificationCode"`
	Secret           string `json:"secret"`
	DeviceInfo       struct {
		OS   string `json:"os"`
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"deviceInfo"`
}

type userPayload struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Note        string `json:"note"`
	Status      int    `json:"status"`
	IsAdmin     bool   `json:"is_admin"`
}

func toPayload(u *store.User) userPayload {
	return userPayload{Name: u.Name, Status: 1, IsAdmin: u.IsAdmin}
}

const (
	msgBadCredentials = "Wrong username or password"
	msgBadCode        = "Wrong or expired verification code"
	msgLocked         = "Too many failed attempts, try again later"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	now := s.now()
	ip := s.clientIP(r)
	userKey := "user:" + strings.ToLower(strings.TrimSpace(req.Username))
	if locked, _ := s.ipLimit.Locked("ip:"+ip, now); locked {
		writeError(w, http.StatusTooManyRequests, msgLocked)
		return
	}
	if locked, _ := s.usLimit.Locked(userKey, now); locked {
		writeError(w, http.StatusTooManyRequests, msgLocked)
		return
	}
	fail := func(msg string) {
		lockedIP := s.ipLimit.Fail("ip:"+ip, now)
		lockedUser := s.usLimit.Fail(userKey, now)
		if lockedIP || lockedUser {
			s.notify("RD login lockout", fmt.Sprintf("Login for %q locked after repeated failures from %s", req.Username, ip))
		}
		writeError(w, http.StatusUnauthorized, msg)
	}

	switch req.Type {
	case "", "account":
		u, err := s.store.GetUserByName(r.Context(), strings.TrimSpace(req.Username))
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.internalError(w, err)
			return
		}
		hash := ""
		if u != nil {
			hash = u.PasswordHash
		}
		if !auth.CheckPasswordOrDummy(hash, req.Password) {
			fail(msgBadCredentials)
			return
		}
		if !u.TOTPEnabled {
			if s.cfg.RequireTOTP {
				writeError(w, http.StatusForbidden,
					"Two-factor authentication is not set up for this account. On the server run: docker compose exec -it api rd-api totp-setup "+u.Name)
				return
			}
			s.finishLogin(w, r, u, req.ID, ip)
			return
		}
		// Password OK: ask for the TOTP code (the client's "tfa_check" dialog).
		secret, _ := auth.NewToken("tfa_")
		s.mu.Lock()
		s.pendingTFA[secret] = pendingTFA{userID: u.ID, clientID: req.ID, expires: now.Add(5 * time.Minute)}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"type":     "email_check",
			"tfa_type": "tfa_check",
			"secret":   secret,
			"user":     toPayload(u),
		})

	case "email_code", "tfa_code":
		s.mu.Lock()
		p, ok := s.pendingTFA[req.Secret]
		if ok && now.After(p.expires) {
			delete(s.pendingTFA, req.Secret)
			ok = false
		}
		s.mu.Unlock()
		if !ok {
			fail(msgBadCode)
			return
		}
		u, err := s.store.GetUserByID(r.Context(), p.userID)
		if err != nil {
			s.internalError(w, err)
			return
		}
		code := req.TfaCode
		if code == "" {
			code = req.VerificationCode
		}
		step, valid := auth.VerifyTOTP(u.TOTPSecret, code, now)
		if !valid {
			fail(msgBadCode)
			return
		}
		fresh, err := s.store.AdvanceTOTPStep(r.Context(), u.ID, step)
		if err != nil {
			s.internalError(w, err)
			return
		}
		if !fresh {
			fail(msgBadCode) // code already used
			return
		}
		s.mu.Lock()
		delete(s.pendingTFA, req.Secret)
		s.mu.Unlock()
		s.finishLogin(w, r, u, p.clientID, ip)

	default:
		writeError(w, http.StatusBadRequest, "Unsupported login type")
	}
}

func (s *Server) finishLogin(w http.ResponseWriter, r *http.Request, u *store.User, clientID, ip string) {
	now := s.now()
	token, hash := auth.NewToken("rds_")
	kind := "client"
	if clientID == "" {
		kind = "web"
	}
	max := s.cfg.SessionMax
	if kind == "web" && max > 12*time.Hour {
		max = 12 * time.Hour // browser sessions are short-lived
	}
	sess := &store.Session{TokenHash: hash, UserID: u.ID, Kind: kind, ClientID: clientID,
		CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(max)}
	if err := s.store.CreateSession(r.Context(), sess); err != nil {
		s.internalError(w, err)
		return
	}
	s.ipLimit.Success("ip:" + ip)
	s.usLimit.Success("user:" + strings.ToLower(u.Name))
	from := ip
	if clientID != "" {
		from = fmt.Sprintf("%s (RustDesk ID %s)", ip, clientID)
	}
	s.cfg.Logger.Info("login", "user", u.Name, "ip", ip, "client_id", clientID)
	s.notify("RD login", fmt.Sprintf("%s logged in from %s", u.Name, from))
	writeJSON(w, http.StatusOK, map[string]any{
		"type":         "access_token",
		"access_token": token,
		"user":         toPayload(u),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if tok := bearer(r); tok != "" {
		s.store.DeleteSession(r.Context(), auth.HashToken(tok))
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) handleCurrentUser(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toPayload(userFrom(r)))
}

func (s *Server) notify(title, msg string) {
	go s.cfg.Notify(title, msg)
}
