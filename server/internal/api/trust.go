package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zeonos/rd/server/internal/store"
)

// "Remember this browser for 30 days": after a successful TOTP login from the
// web console the server sets an HttpOnly cookie. While it is valid, the
// password alone logs that user in from that browser. The cookie is an HMAC
// over the user ID, the expiry and a hash of the user's password hash and
// TOTP secret, so changing either revokes every remembered browser.

const (
	trustCookie  = "__Host-rd_trust"
	trustTTL     = 30 * 24 * time.Hour
	trustKeyName = "trust-hmac-key"
)

func (s *Server) trustKey(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	k := s.trustMAC
	s.mu.Unlock()
	if k != nil {
		return k, nil
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	v, err := s.store.PutServerKeyIfAbsent(ctx, trustKeyName, base64.StdEncoding.EncodeToString(seed))
	if err != nil {
		return nil, err
	}
	k, err = base64.StdEncoding.DecodeString(v)
	if err != nil || len(k) < 32 {
		return nil, fmt.Errorf("stored trust key is corrupt")
	}
	s.mu.Lock()
	s.trustMAC = k
	s.mu.Unlock()
	return k, nil
}

func trustMAC(key []byte, u *store.User, exp int64) string {
	stamp := sha256.Sum256([]byte(u.PasswordHash + "\n" + u.TOTPSecret))
	m := hmac.New(sha256.New, key)
	fmt.Fprintf(m, "rd-trust\n%d\n%d\n%s", u.ID, exp, hex.EncodeToString(stamp[:]))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// setTrustCookie remembers this browser for u.
func (s *Server) setTrustCookie(w http.ResponseWriter, r *http.Request, u *store.User) {
	key, err := s.trustKey(r.Context())
	if err != nil {
		s.cfg.Logger.Error("trust key", "err", err)
		return
	}
	exp := s.now().Add(trustTTL).Unix()
	http.SetCookie(w, &http.Cookie{
		Name:     trustCookie,
		Value:    fmt.Sprintf("%d.%d.%s", u.ID, exp, trustMAC(key, u, exp)),
		Path:     "/",
		Expires:  time.Unix(exp, 0),
		MaxAge:   int(trustTTL / time.Second),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// trusted reports whether the request carries a valid remember-browser
// cookie for u.
func (s *Server) trusted(r *http.Request, u *store.User) bool {
	c, err := r.Cookie(trustCookie)
	if err != nil {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 {
		return false
	}
	id, err1 := strconv.ParseInt(parts[0], 10, 64)
	exp, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil || id != u.ID || s.now().Unix() >= exp || exp > s.now().Add(trustTTL+time.Hour).Unix() {
		return false
	}
	key, err := s.trustKey(r.Context())
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(parts[2]), []byte(trustMAC(key, u, exp)))
}

// handleForgetBrowser removes the remember-browser cookie.
func (s *Server) handleForgetBrowser(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: trustCookie, Value: "", Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}
