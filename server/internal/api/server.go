// Package api implements the HTTP API: the endpoints the RustDesk client
// expects from an "API server", plus admin endpoints for the web console.
package api

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zeonos/rd/server/internal/auth"
	"github.com/zeonos/rd/server/internal/store"
)

type Config struct {
	// RequireTOTP refuses logins for users without TOTP set up.
	RequireTOTP bool
	// TrustProxy takes the client IP from the last X-Forwarded-For entry (Caddy).
	TrustProxy bool
	// SessionIdle / SessionMax bound login sessions.
	SessionIdle time.Duration
	SessionMax  time.Duration
	// Notify is called (asynchronously) on security events such as logins.
	Notify func(title, message string)
	Logger *slog.Logger
}

type Server struct {
	store   store.Store
	cfg     Config
	now     func() time.Time
	ipLimit *auth.Limiter
	usLimit *auth.Limiter

	mu         sync.Mutex
	pendingTFA map[string]pendingTFA // secret -> pending second factor
	signKey    ed25519.PrivateKey    // job signing key, loaded on first use

	mux *http.ServeMux
}

type pendingTFA struct {
	userID   int64
	clientID string
	expires  time.Time
}

func New(s store.Store, cfg Config) *Server {
	if cfg.SessionIdle == 0 {
		cfg.SessionIdle = 7 * 24 * time.Hour
	}
	if cfg.SessionMax == 0 {
		cfg.SessionMax = 30 * 24 * time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Notify == nil {
		cfg.Notify = func(string, string) {}
	}
	srv := &Server{
		store:      s,
		cfg:        cfg,
		now:        time.Now,
		ipLimit:    auth.NewLimiter(5, 15*time.Minute, 15*time.Minute),
		usLimit:    auth.NewLimiter(10, 15*time.Minute, 15*time.Minute),
		pendingTFA: map[string]pendingTFA{},
		mux:        http.NewServeMux(),
	}
	srv.routes()
	return srv
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	// Account (RustDesk client compatible)
	m.HandleFunc("POST /api/login", s.handleLogin)
	m.HandleFunc("GET /api/login-options", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, []string{}) })
	m.HandleFunc("POST /api/logout", s.handleLogout)
	m.HandleFunc("POST /api/currentUser", s.requireUser(s.handleCurrentUser))

	// Device side (RustDesk client compatible, unauthenticated by design;
	// a device must have been enrolled with a token first)
	m.HandleFunc("POST /api/heartbeat", s.handleHeartbeat)
	m.HandleFunc("POST /api/sysinfo", s.handleSysinfo)
	m.HandleFunc("POST /api/sysinfo_ver", func(w http.ResponseWriter, r *http.Request) { w.Write(nil) })
	m.HandleFunc("POST /api/devices/cli", s.handleEnroll)

	// Agent job channel (signed both ways, see jobs.go)
	m.HandleFunc("POST /api/agent/poll", s.handleAgentPoll)
	m.HandleFunc("POST /api/agent/result", s.handleAgentResult)
	m.HandleFunc("GET /api/agent/key", s.handleAgentKey)

	// Address book (legacy mode) and the "accessible devices" group tab
	m.HandleFunc("GET /api/ab", s.requireUser(s.handleGetAB))
	m.HandleFunc("POST /api/ab", s.requireUser(s.handlePostAB))
	m.HandleFunc("GET /api/device-group/accessible", s.requireUser(s.handleDeviceGroups))
	m.HandleFunc("GET /api/users", s.requireUser(s.handleUsers))
	m.HandleFunc("GET /api/peers", s.requireUser(s.handlePeers))

	// Admin API (web console, scripts)
	m.HandleFunc("GET /api/admin/devices", s.requireAdmin(s.adminListDevices))
	m.HandleFunc("PATCH /api/admin/devices/{id}", s.requireAdmin(s.adminUpdateDevice))
	m.HandleFunc("DELETE /api/admin/devices/{id}", s.requireAdmin(s.adminDeleteDevice))
	m.HandleFunc("GET /api/admin/groups", s.requireAdmin(s.adminListGroups))
	m.HandleFunc("POST /api/admin/groups", s.requireAdmin(s.adminCreateGroup))
	m.HandleFunc("PATCH /api/admin/groups/{id}", s.requireAdmin(s.adminRenameGroup))
	m.HandleFunc("DELETE /api/admin/groups/{id}", s.requireAdmin(s.adminDeleteGroup))
	m.HandleFunc("GET /api/admin/tokens", s.requireAdmin(s.adminListTokens))
	m.HandleFunc("POST /api/admin/tokens", s.requireAdmin(s.adminCreateToken))
	m.HandleFunc("DELETE /api/admin/tokens/{id}", s.requireAdmin(s.adminDeleteToken))
	m.HandleFunc("GET /api/admin/policy/catalogue", s.requireAdmin(s.adminPolicyCatalogue))
	m.HandleFunc("GET /api/admin/policies", s.requireAdmin(s.adminListPolicies))
	m.HandleFunc("PUT /api/admin/policies/{scope}", s.requireAdmin(s.adminSetPolicy))
	m.HandleFunc("GET /api/admin/devices/{id}/policy", s.requireAdmin(s.adminDevicePolicy))
	m.HandleFunc("GET /api/admin/scripts", s.requireAdmin(s.adminListScripts))
	m.HandleFunc("POST /api/admin/scripts", s.requireAdmin(s.adminSaveScript))
	m.HandleFunc("PUT /api/admin/scripts/{id}", s.requireAdmin(s.adminSaveScript))
	m.HandleFunc("DELETE /api/admin/scripts/{id}", s.requireAdmin(s.adminDeleteScript))
	m.HandleFunc("GET /api/admin/jobs", s.requireAdmin(s.adminListJobs))
	m.HandleFunc("POST /api/admin/jobs", s.requireAdmin(s.adminCreateJob))
	m.HandleFunc("GET /api/admin/jobs/{id}", s.requireAdmin(s.adminGetJob))
	m.HandleFunc("POST /api/admin/jobs/{id}/cancel", s.requireAdmin(s.adminCancelJob))

	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("RD API server. The web console arrives in phase 2.\n"))
	})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: 200}
	s.mux.ServeHTTP(rec, r)
	if r.URL.Path != "/api/heartbeat" && r.URL.Path != "/health" {
		s.cfg.Logger.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"ip", s.clientIP(r), "ms", time.Since(start).Milliseconds())
	}
}

// Maintain runs periodic cleanup until ctx is done.
func (s *Server) Maintain(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for tick := 0; ; tick++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := s.now()
			if err := s.store.ExpireRuns(ctx, now); err != nil {
				s.cfg.Logger.Error("expire job runs", "err", err)
			}
			if tick%10 != 0 {
				continue
			}
			s.ipLimit.Sweep(now)
			s.usLimit.Sweep(now)
			s.mu.Lock()
			for k, p := range s.pendingTFA {
				if now.After(p.expires) {
					delete(s.pendingTFA, k)
				}
			}
			s.mu.Unlock()
			if err := s.store.DeleteExpiredSessions(ctx, now, s.cfg.SessionIdle); err != nil {
				s.cfg.Logger.Error("session cleanup", "err", err)
			}
		}
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// --- helpers ---

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeText(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(msg))
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	return dec.Decode(v)
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

type ctxKey int

const userKey ctxKey = 0

func userFrom(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey).(*store.User)
	return u
}

// authenticate resolves the bearer token to a user, enforcing session expiry.
func (s *Server) authenticate(r *http.Request) (*store.User, *store.Session, error) {
	tok := bearer(r)
	if tok == "" {
		return nil, nil, store.ErrNotFound
	}
	h := auth.HashToken(tok)
	sess, err := s.store.GetSession(r.Context(), h)
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	if !now.Before(sess.ExpiresAt) || now.Sub(sess.LastSeen) > s.cfg.SessionIdle {
		s.store.DeleteSession(r.Context(), h)
		return nil, nil, store.ErrNotFound
	}
	u, err := s.store.GetUserByID(r.Context(), sess.UserID)
	if err != nil {
		return nil, nil, err
	}
	if now.Sub(sess.LastSeen) > time.Minute {
		s.store.TouchSession(r.Context(), h, now)
	}
	return u, sess, nil
}

func (s *Server) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, _, err := s.authenticate(r)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.cfg.Logger.Error("auth", "err", err)
			}
			writeError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	}
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if !userFrom(r).IsAdmin {
			writeError(w, http.StatusForbidden, "Admin required!")
			return
		}
		next(w, r)
	})
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.cfg.Logger.Error("internal", "err", err)
	writeError(w, http.StatusInternalServerError, "Internal error")
}
