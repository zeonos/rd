package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zeonos/rd/server/internal/store"
)

func releaseZip(t *testing.T, build string, withExe bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{"install.ps1": "#", "rd/rd-build.txt": build, "rd/rd-version.txt": "1.5.3"}
	if withExe {
		files["rd/RD.exe"] = "MZ"
	}
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

func (e *env) upload(token string, body []byte) (int, string) {
	req := httptest.NewRequest("POST", "/api/admin/releases", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestReleasesAndAutoUpdate(t *testing.T) {
	e := newEnv(t)
	e.srv.cfg.DataDir = t.TempDir()
	tok := e.login()
	ctx := context.Background()

	if code, _ := e.upload(tok, releaseZip(t, "7", false)); code != 400 {
		t.Fatal("zip without RD.exe accepted")
	}
	zipBytes := releaseZip(t, "7", true)
	code, body := e.upload(tok, zipBytes)
	if code != 200 {
		t.Fatalf("upload: %d %s", code, body)
	}
	sum := sha256.Sum256(zipBytes)
	if decode(t, body)["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256: %s", body)
	}
	if code, _ := e.upload(tok, zipBytes); code != 409 {
		t.Fatal("duplicate build accepted")
	}

	// devices: one on build 6 (online), one before auto-update (build 0), one current
	for id, build := range map[string]string{"1": "6", "2": "", "3": "7"} {
		e.st.EnrollDevice(ctx, &store.Device{ID: id, UUID: "u" + id})
		e.do("POST", "/api/sysinfo", "", map[string]any{"id": id, "uuid": "u" + id, "hostname": "H" + id, "rd_build": build})
		e.st.Heartbeat(ctx, id, 0, e.now)
	}

	// nothing rolls out until a release is current
	e.srv.autoUpdate(ctx, e.now)
	if _, body := e.do("GET", "/api/admin/jobs", tok, nil); strings.Contains(body, "Update to build") {
		t.Fatal("update queued without a current release")
	}
	if code, _ := e.do("PUT", "/api/admin/releases/current", tok, map[string]any{"build": 7}); code != 200 {
		t.Fatal("set current failed")
	}
	if code, _ := e.do("DELETE", "/api/admin/releases/7", tok, nil); code != 409 {
		t.Fatal("deleted the current release")
	}
	e.srv.autoUpdate(ctx, e.now)
	e.srv.autoUpdate(ctx, e.now.Add(time.Minute)) // no second job within the retry window
	_, body = e.do("GET", "/api/admin/jobs", tok, nil)
	var jobs []store.Job
	json.Unmarshal([]byte(body), &jobs)
	if len(jobs) != 1 || jobs[0].Kind != "update" || jobs[0].Target != "H1" {
		t.Fatalf("update jobs: %s", body)
	}
	var ub UpdateBody
	json.Unmarshal([]byte(jobs[0].Body), &ub)
	if ub.Build != 7 || ub.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("update body: %+v", ub)
	}

	// the agent downloads the zip without a login
	req := httptest.NewRequest("GET", ub.Path, nil)
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), zipBytes) {
		t.Fatalf("download: %d", rec.Code)
	}

	_, body = e.do("GET", "/api/admin/releases", tok, nil)
	if !strings.Contains(body, `"devices":1`) || !strings.Contains(body, `"unknown_build_devices":1`) {
		t.Fatalf("release list: %s", body)
	}
}

func TestInventoryAndDiskAlert(t *testing.T) {
	e := newEnv(t)
	e.srv.cfg.DiskAlertPercent = 10
	tok := e.login()
	ctx := context.Background()
	e.st.EnrollDevice(ctx, &store.Device{ID: "5", UUID: "u5"})
	pub, priv, _ := ed25519.GenerateKey(nil)
	e.do("POST", "/api/sysinfo", "", map[string]any{"id": "5", "uuid": "u5", "hostname": "SRV5", "rd_pk": base64.StdEncoding.EncodeToString(pub)})

	ts := strconv.FormatInt(e.now.Unix(), 10)
	inv := `{"disks":[{"name":"C:","size":100000000000,"free":5000000000},{"name":"D:","size":100,"free":50}]}`
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(InventoryMessage("5", ts, inv))))
	if code, body := e.do("POST", "/api/agent/inventory", "", map[string]any{"id": "5", "ts": ts, "inventory": inv, "sig": sig}); code != 200 {
		t.Fatalf("inventory: %d %s", code, body)
	}
	if code, _ := e.do("POST", "/api/agent/inventory", "", map[string]any{"id": "5", "ts": ts, "inventory": `{"disks":[]}`, "sig": sig}); code != 401 {
		t.Fatal("inventory with a signature for other content accepted")
	}
	_, body := e.do("GET", "/api/admin/devices/5/inventory", tok, nil)
	if body != inv {
		t.Fatalf("stored inventory: %s", body)
	}
	_, body = e.do("GET", "/api/admin/devices", tok, nil)
	if !strings.Contains(body, `"inventory_at"`) || !strings.Contains(body, `"ip":`) ||
		!strings.Contains(body, `"disks":[{"name":"C:","size":100000000000,"free":5000000000},{"name":"D:","size":100,"free":50}]`) {
		t.Fatalf("device list: %s", body)
	}

	// a second report the same day does not alert again
	e.do("POST", "/api/agent/inventory", "", map[string]any{"id": "5", "ts": ts, "inventory": inv, "sig": sig})
	time.Sleep(20 * time.Millisecond)
	e.mu.Lock()
	n := 0
	for _, x := range e.notes {
		if x == "RD: low disk space" {
			n++
		}
	}
	e.mu.Unlock()
	if n != 1 {
		t.Fatalf("low disk alerts: %d", n)
	}
}

func TestHeartbeatRecordsIP(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.st.EnrollDevice(ctx, &store.Device{ID: "9", UUID: "u9"})
	e.do("POST", "/api/heartbeat", "", map[string]any{"id": "9", "uuid": "u9"})
	if d, _ := e.st.GetDevice(ctx, "9"); d.IP != "198.51.100.7" {
		t.Fatalf("ip: %q", d.IP)
	}
}

func TestBrowserViewerServed(t *testing.T) {
	e := newEnv(t)
	code, body := e.do("GET", "/web/", "", nil)
	if code != 200 || !strings.Contains(body, `src="app.js"`) {
		t.Fatalf("viewer page: %d %s", code, body)
	}
	if code, _ := e.do("GET", "/web/app.js", "", nil); code != 200 {
		t.Fatal("viewer script missing")
	}
	if code, _ := e.do("GET", "/web", "", nil); code != 301 {
		t.Fatalf("/web should redirect, got %d", code)
	}
}

func TestInstallScript(t *testing.T) {
	e := newEnv(t)
	e.srv.cfg.DataDir = t.TempDir()
	tok := e.login()
	if _, body := e.do("GET", "/install?token=rde_abc", "", nil); !strings.Contains(body, "No RD client build") {
		t.Fatalf("no release: %s", body)
	}
	zipBytes := releaseZip(t, "21", true)
	e.upload(tok, zipBytes)
	sum := sha256.Sum256(zipBytes)
	code, body := e.do("GET", "/install?token=rde_abc&group=It's/Room&ws=1", "", nil)
	if code != 200 || !strings.Contains(body, "/api/releases/21.zip") || !strings.Contains(body, strings.ToUpper(hex.EncodeToString(sum[:]))) ||
		!strings.Contains(body, "[string]$Token = 'rde_abc'") || !strings.Contains(body, "[string]$Group = 'It''s/Room'") ||
		!strings.Contains(body, "[switch]$WebSocket = $true") {
		t.Fatalf("script: %d\n%s", code, body)
	}
	if code, _ := e.do("GET", "/install?token=x%27%2Bcalc%2B%27", "", nil); code != 400 {
		t.Fatal("token with quotes accepted")
	}
}
