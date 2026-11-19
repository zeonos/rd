package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zeonos/rd/server/internal/auth"
)

func (e *env) loginCookie(body map[string]any, cookie *http.Cookie) (int, map[string]any, *http.Cookie) {
	e.t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/login", bytes.NewReader(b))
	req.RemoteAddr = "198.51.100.7:5555"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	var m map[string]any
	json.Unmarshal(rec.Body.Bytes(), &m)
	var set *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == trustCookie {
			set = c
		}
	}
	return rec.Code, m, set
}

func TestRememberBrowser(t *testing.T) {
	e := newEnv(t)
	pw := map[string]any{"username": "admin", "password": "pw", "type": "account"}

	// First login: TOTP asked; ticking "remember" sets the cookie.
	_, m, _ := e.loginCookie(pw, nil)
	if m["type"] != "email_check" {
		t.Fatalf("expected TOTP challenge, got %v", m)
	}
	otp, _ := auth.TOTPCodeAt(e.secret, e.now)
	code, m, cookie := e.loginCookie(map[string]any{"type": "email_code", "tfaCode": otp, "secret": m["secret"], "remember": true}, nil)
	if code != 200 || m["type"] != "access_token" || cookie == nil {
		t.Fatalf("login with remember: %d %v cookie=%v", code, m, cookie)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("cookie attributes: %+v", cookie)
	}

	// With the cookie the password alone is enough.
	e.now = e.now.Add(24 * time.Hour)
	if _, m, _ = e.loginCookie(pw, cookie); m["type"] != "access_token" {
		t.Fatalf("remembered browser still asked for TOTP: %v", m)
	}
	// ...but never without the password.
	if code, _, _ := e.loginCookie(map[string]any{"username": "admin", "password": "nope", "type": "account"}, cookie); code != 401 {
		t.Fatalf("wrong password with cookie: %d", code)
	}
	// The RustDesk client (sends its ID) always gets the TOTP challenge.
	if _, m, _ = e.loginCookie(map[string]any{"username": "admin", "password": "pw", "type": "account", "id": "123"}, cookie); m["type"] != "email_check" {
		t.Fatalf("client login skipped TOTP: %v", m)
	}
	// A tampered cookie is ignored.
	bad := *cookie
	bad.Value = bad.Value[:len(bad.Value)-2] + "AA"
	if _, m, _ = e.loginCookie(pw, &bad); m["type"] != "email_check" {
		t.Fatalf("tampered cookie accepted: %v", m)
	}
	// After 30 days it expires.
	e.now = e.now.Add(30 * 24 * time.Hour)
	if _, m, _ = e.loginCookie(pw, cookie); m["type"] != "email_check" {
		t.Fatalf("expired cookie accepted: %v", m)
	}
}

func TestRememberBrowserRevokedOnTOTPChange(t *testing.T) {
	e := newEnv(t)
	pw := map[string]any{"username": "admin", "password": "pw", "type": "account"}
	_, m, _ := e.loginCookie(pw, nil)
	otp, _ := auth.TOTPCodeAt(e.secret, e.now)
	_, _, cookie := e.loginCookie(map[string]any{"type": "email_code", "tfaCode": otp, "secret": m["secret"], "remember": true}, nil)
	if cookie == nil {
		t.Fatal("no cookie")
	}
	u, _ := e.st.GetUserByName(context.Background(), "admin")
	e.st.SetUserTOTP(context.Background(), u.ID, auth.NewTOTPSecret(), true)
	if _, m, _ = e.loginCookie(pw, cookie); m["type"] != "email_check" {
		t.Fatalf("cookie survived a TOTP reset: %v", m)
	}
}
