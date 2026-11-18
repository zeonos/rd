package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeonos/rd/server/internal/auth"
	"github.com/zeonos/rd/server/internal/store"
)

type env struct {
	t      *testing.T
	srv    *Server
	st     *store.Memory
	now    time.Time
	secret string
	mu     sync.Mutex
	notes  []string
}

func newEnv(t *testing.T) *env {
	e := &env{t: t, st: store.NewMemory(), now: time.Unix(1_800_000_000, 0)}
	e.srv = New(e.st, Config{
		RequireTOTP: true,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notify: func(title, msg string) {
			e.mu.Lock()
			e.notes = append(e.notes, title)
			e.mu.Unlock()
		},
	})
	e.srv.now = func() time.Time { return e.now }
	hash, _ := auth.HashPassword("pw")
	e.secret = auth.NewTOTPSecret()
	u := &store.User{Name: "admin", PasswordHash: hash, IsAdmin: true}
	if err := e.st.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	e.st.SetUserTOTP(context.Background(), u.ID, e.secret, true)
	return e
}

func (e *env) do(method, path, token string, body any) (int, string) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	req.RemoteAddr = "198.51.100.7:5555"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return m
}

func (e *env) login() string {
	e.t.Helper()
	code, body := e.do("POST", "/api/login", "", map[string]any{"username": "admin", "password": "pw", "type": "account", "id": "999"})
	if code != 200 {
		e.t.Fatalf("login step 1: %d %s", code, body)
	}
	m := decode(e.t, body)
	if m["type"] != "email_check" || m["tfa_type"] != "tfa_check" {
		e.t.Fatalf("expected tfa challenge, got %v", m)
	}
	otp, _ := auth.TOTPCodeAt(e.secret, e.now)
	code, body = e.do("POST", "/api/login", "", map[string]any{"username": "admin", "type": "email_code", "tfaCode": otp, "secret": m["secret"]})
	if code != 200 {
		e.t.Fatalf("login step 2: %d %s", code, body)
	}
	m = decode(e.t, body)
	if m["type"] != "access_token" || m["access_token"] == "" {
		e.t.Fatalf("no token: %v", m)
	}
	e.now = e.now.Add(31 * time.Second) // next login needs a fresh TOTP step
	return m["access_token"].(string)
}

func TestLoginFlow(t *testing.T) {
	e := newEnv(t)
	tok := e.login()

	code, body := e.do("POST", "/api/currentUser", tok, map[string]string{})
	if code != 200 || decode(t, body)["name"] != "admin" {
		t.Fatalf("currentUser: %d %s", code, body)
	}
	if code, _ := e.do("POST", "/api/currentUser", "bogus", nil); code != 401 {
		t.Fatalf("bad token accepted: %d", code)
	}
	e.do("POST", "/api/logout", tok, map[string]string{})
	if code, _ := e.do("POST", "/api/currentUser", tok, nil); code != 401 {
		t.Fatal("token valid after logout")
	}
}

func TestTOTPReplayAndBadCode(t *testing.T) {
	e := newEnv(t)
	_, body := e.do("POST", "/api/login", "", map[string]any{"username": "admin", "password": "pw", "type": "account"})
	sec := decode(t, body)["secret"]
	if code, _ := e.do("POST", "/api/login", "", map[string]any{"type": "email_code", "tfaCode": "000000", "secret": sec}); code != 401 {
		t.Fatal("wrong code accepted")
	}
	otp, _ := auth.TOTPCodeAt(e.secret, e.now)
	if code, _ := e.do("POST", "/api/login", "", map[string]any{"type": "email_code", "tfaCode": otp, "secret": sec}); code != 200 {
		t.Fatal("right code rejected")
	}
	// same code again via a new challenge must fail (replay)
	_, body = e.do("POST", "/api/login", "", map[string]any{"username": "admin", "password": "pw", "type": "account"})
	sec = decode(t, body)["secret"]
	if code, _ := e.do("POST", "/api/login", "", map[string]any{"type": "email_code", "tfaCode": otp, "secret": sec}); code != 401 {
		t.Fatal("replayed TOTP code accepted")
	}
}

func TestLockout(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		e.do("POST", "/api/login", "", map[string]any{"username": "admin", "password": "wrong", "type": "account"})
	}
	code, _ := e.do("POST", "/api/login", "", map[string]any{"username": "admin", "password": "pw", "type": "account"})
	if code != 429 {
		t.Fatalf("expected lockout, got %d", code)
	}
	e.now = e.now.Add(16 * time.Minute)
	code, _ = e.do("POST", "/api/login", "", map[string]any{"username": "admin", "password": "pw", "type": "account"})
	if code != 200 {
		t.Fatalf("lock should expire, got %d", code)
	}
}

func TestNoTOTPRefused(t *testing.T) {
	e := newEnv(t)
	hash, _ := auth.HashPassword("pw2")
	e.st.CreateUser(context.Background(), &store.User{Name: "bob", PasswordHash: hash})
	code, body := e.do("POST", "/api/login", "", map[string]any{"username": "bob", "password": "pw2", "type": "account"})
	if code != 403 || !strings.Contains(body, "totp-setup") {
		t.Fatalf("login without TOTP: %d %s", code, body)
	}
}

func TestDeviceLifecycle(t *testing.T) {
	e := newEnv(t)
	tok := e.login()

	// admin creates an enrolment token for a location
	code, body := e.do("POST", "/api/admin/tokens", tok, map[string]any{"group": "Office/Rack 1", "max_uses": 2})
	if code != 200 {
		t.Fatalf("create token: %d %s", code, body)
	}
	enroll := decode(t, body)["token"].(string)

	// unknown device: heartbeat ignored, sysinfo says not enrolled
	_, body = e.do("POST", "/api/heartbeat", "", map[string]any{"id": "111222333", "uuid": "dXVpZA=="})
	if body != "{}\n" {
		t.Fatalf("heartbeat unknown: %q", body)
	}
	if _, body := e.do("POST", "/api/sysinfo", "", map[string]any{"id": "111222333", "uuid": "dXVpZA=="}); body != "NOT_ENROLLED" {
		t.Fatalf("sysinfo unknown: %q", body)
	}

	// enrolment: bad token, then good token
	if code, _ := e.do("POST", "/api/devices/cli", "rde_nope", map[string]any{"id": "111222333", "uuid": "dXVpZA=="}); code != 401 {
		t.Fatalf("bad enrolment token: %d", code)
	}
	code, body = e.do("POST", "/api/devices/cli", enroll, map[string]any{"id": "111222333", "uuid": "dXVpZA==", "device_group_name": "ignored"})
	if code != 200 || body != "" {
		t.Fatalf("enrol: %d %q", code, body)
	}

	// first heartbeat asks for sysinfo
	_, body = e.do("POST", "/api/heartbeat", "", map[string]any{"id": "111222333", "uuid": "dXVpZA==", "conns": []int{}})
	if decode(t, body)["sysinfo"] != true {
		t.Fatalf("expected sysinfo request: %s", body)
	}
	h1 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	_, body = e.do("POST", "/api/sysinfo", "", map[string]any{"id": "111222333", "uuid": "dXVpZA==",
		"hostname": "SRV-SQL1", "os": "Windows Server 2022 Standard", "username": "Administrator", "rd_pw_h1": h1})
	if body != "SYSINFO_UPDATED" {
		t.Fatalf("sysinfo: %q", body)
	}
	// wrong uuid cannot overwrite
	if _, body := e.do("POST", "/api/sysinfo", "", map[string]any{"id": "111222333", "uuid": "b3RoZXI=", "rd_pw_h1": h1}); body != "UUID_MISMATCH" {
		t.Fatalf("uuid mismatch: %q", body)
	}
	_, body = e.do("POST", "/api/heartbeat", "", map[string]any{"id": "111222333", "uuid": "dXVpZA==", "conns": []int{5}})
	if body != "{}\n" {
		t.Fatalf("second heartbeat: %q", body)
	}

	// address book carries the hash and the location tag
	code, body = e.do("GET", "/api/ab", tok, nil)
	if code != 200 {
		t.Fatalf("ab: %d %s", code, body)
	}
	var ab struct {
		Tags  []string `json:"tags"`
		Peers []abPeer `json:"peers"`
	}
	if err := json.Unmarshal([]byte(decode(t, body)["data"].(string)), &ab); err != nil {
		t.Fatal(err)
	}
	if len(ab.Peers) != 1 || ab.Peers[0].Hash != h1 || ab.Peers[0].Tags[0] != "Office/Rack 1" || ab.Peers[0].Platform != "Windows" {
		t.Fatalf("ab peers: %+v", ab.Peers)
	}
	if code, _ := e.do("GET", "/api/ab", "", nil); code != 401 {
		t.Fatal("ab without login")
	}

	// group tab
	_, body = e.do("GET", "/api/peers?current=1&pageSize=100", tok, nil)
	if !strings.Contains(body, `"device_group_name":"Office/Rack 1"`) || !strings.Contains(body, `"total":1`) {
		t.Fatalf("peers: %s", body)
	}
	_, body = e.do("GET", "/api/device-group/accessible?current=1&pageSize=100", tok, nil)
	if !strings.Contains(body, `"Office/Rack 1"`) {
		t.Fatalf("device groups: %s", body)
	}

	// admin: list, move, rename
	_, body = e.do("GET", "/api/admin/devices", tok, nil)
	if !strings.Contains(body, `"online":true`) || !strings.Contains(body, `"unattended":true`) {
		t.Fatalf("admin devices: %s", body)
	}
	code, body = e.do("PATCH", "/api/admin/devices/111222333", tok, map[string]any{"group": "Datacenter", "alias": "SQL"})
	if code != 200 || !strings.Contains(body, `"group":"Datacenter"`) {
		t.Fatalf("move device: %d %s", code, body)
	}
	// device goes offline after 60 s without heartbeat
	e.now = e.now.Add(2 * time.Minute)
	_, body = e.do("GET", "/api/admin/devices", tok, nil)
	if !strings.Contains(body, `"online":false`) {
		t.Fatalf("offline: %s", body)
	}
	if code, _ := e.do("DELETE", "/api/admin/devices/111222333", tok, nil); code != 204 {
		t.Fatalf("delete device: %d", code)
	}
}

func TestEnrollTokenWithoutGroupUsesDeviceChoice(t *testing.T) {
	e := newEnv(t)
	tok := e.login()
	_, body := e.do("POST", "/api/admin/tokens", tok, map[string]any{"expires_in_hours": 1})
	enroll := decode(t, body)["token"].(string)
	if code, body := e.do("POST", "/api/devices/cli", enroll, map[string]any{"id": "5", "uuid": "u", "device_group_name": "Home lab"}); code != 200 {
		t.Fatalf("enrol: %d %s", code, body)
	}
	d, _ := e.st.GetDevice(context.Background(), "5")
	if d.GroupPath != "Home lab" {
		t.Fatalf("group: %q", d.GroupPath)
	}
	e.now = e.now.Add(2 * time.Hour)
	if code, _ := e.do("POST", "/api/devices/cli", enroll, map[string]any{"id": "6", "uuid": "u"}); code != 401 {
		t.Fatal("expired token accepted")
	}
}

func TestAdminRequired(t *testing.T) {
	e := newEnv(t)
	hash, _ := auth.HashPassword("pw")
	u := &store.User{Name: "viewer", PasswordHash: hash}
	e.st.CreateUser(context.Background(), u)
	sess := &store.Session{TokenHash: auth.HashToken("t"), UserID: u.ID, CreatedAt: e.now, LastSeen: e.now, ExpiresAt: e.now.Add(time.Hour)}
	e.st.CreateSession(context.Background(), sess)
	if code, _ := e.do("GET", "/api/admin/devices", "t", nil); code != 403 {
		t.Fatalf("non-admin got %d", code)
	}
}

func TestSessionIdleExpiry(t *testing.T) {
	e := newEnv(t)
	tok := e.login()
	e.now = e.now.Add(8 * 24 * time.Hour)
	if code, _ := e.do("POST", "/api/currentUser", tok, nil); code != 401 {
		t.Fatal("idle session still valid")
	}
}

func TestClientIPTrustProxy(t *testing.T) {
	e := newEnv(t)
	e.srv.cfg.TrustProxy = true
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "172.18.0.2:1234"
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if ip := e.srv.clientIP(req); ip != "203.0.113.9" {
		t.Fatalf("ip %s", ip)
	}
	_ = http.StatusOK
}
