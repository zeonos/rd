package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zeonos/rd/server/internal/store"
)

const replicaReport = `{"found":true,"version":"v1.29.2","my_id":"AAA-1","my_name":"srv-b1903",
 "folders":[{"id":"data","label":"Data","type":"sendonly","state":"idle","global_bytes":1000,"in_sync_bytes":1000,
   "devices":[{"id":"BBB-2","completion":100,"remote_state":"valid"},{"id":"CCC-3","completion":75.5,"need_bytes":250,"remote_state":"valid"},
              {"id":"DDD-4","completion":40,"remote_state":"valid"}]}],
 "devices":[{"id":"BBB-2","name":"SRV-Baune","connected":true},{"id":"CCC-3","name":"srv-lundtofte","connected":true},
            {"id":"DDD-4","name":"vaenget-srv","connected":false,"last_seen":"2026-11-18T10:00:00Z"}]}`

func TestSummarize(t *testing.T) {
	var rep SyncReport
	if err := json.Unmarshal([]byte(replicaReport), &rep); err != nil {
		t.Fatal(err)
	}
	level, text, detail := Summarize(&rep)
	if level != "warn" || !strings.HasPrefix(text, "vaenget-srv is not connected") || len(detail) != 2 || detail[1] != "srv-lundtofte 75%" {
		t.Fatalf("%s %q %q", level, text, detail)
	}
	rep.Devices[2].Connected = true
	if level, text, _ = Summarize(&rep); level != "syncing" || text != "vaenget-srv 40% (+1 more)" {
		t.Fatalf("%s %q", level, text)
	}
	for i := range rep.Folders[0].Peers {
		rep.Folders[0].Peers[i].Completion = 100
	}
	if level, text, _ = Summarize(&rep); level != "ok" || text != "In sync (1 folder)" {
		t.Fatalf("%s %q", level, text)
	}
	rep.Folders[0].Errors = 3
	if level, _, _ = Summarize(&rep); level != "error" {
		t.Fatal("errors not reported")
	}
	if level, text, _ = Summarize(&SyncReport{Found: false}); level != "none" || text != "Not installed" {
		t.Fatal(level, text)
	}
}

func TestAgentSync(t *testing.T) {
	e := newEnv(t)
	e.srv.cfg.SyncAlert = time.Hour
	tok := e.login()
	ctx := context.Background()
	e.st.EnrollDevice(ctx, &store.Device{ID: "5", UUID: "u5"})
	pub, priv, _ := ed25519.GenerateKey(nil)
	e.do("POST", "/api/sysinfo", "", map[string]any{"id": "5", "uuid": "u5", "hostname": "SRV-B1903", "rd_pk": base64.StdEncoding.EncodeToString(pub)})
	post := func(report string) int {
		ts := strconv.FormatInt(e.now.Unix(), 10)
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(SyncMessage("5", ts, report))))
		code, _ := e.do("POST", "/api/agent/sync", "", map[string]any{"id": "5", "ts": ts, "report": report, "sig": sig})
		return code
	}
	if code, _ := e.do("POST", "/api/agent/sync", "", map[string]any{"id": "5", "ts": "1", "report": "{}", "sig": "x"}); code != 401 {
		t.Fatalf("unsigned report accepted: %d", code)
	}
	if code := post(replicaReport); code != 200 {
		t.Fatalf("post: %d", code)
	}
	_, body := e.do("GET", "/api/admin/devices", tok, nil)
	if !strings.Contains(body, `"level":"warn"`) || !strings.Contains(body, "vaenget-srv is not connected") {
		t.Fatalf("summary missing: %s", body)
	}
	_, body = e.do("GET", "/api/admin/devices/5/sync", tok, nil)
	if !strings.Contains(body, `"my_name":"srv-b1903"`) {
		t.Fatalf("detail: %s", body)
	}
	// still out of sync an hour later: one alert; in sync again: one more
	e.now = e.now.Add(61 * time.Minute)
	post(replicaReport)
	post(replicaReport)
	ok := strings.ReplaceAll(strings.ReplaceAll(replicaReport, `"completion":75.5`, `"completion":100`), `"completion":40`, `"completion":100`)
	post(ok)
	var notes string
	for i := 0; i < 100 && strings.Count(notes, "RD: ") < 3; i++ {
		time.Sleep(5 * time.Millisecond)
		e.mu.Lock()
		notes = strings.Join(e.notes, "|")
		e.mu.Unlock()
	}
	if strings.Count(notes, "RD: out of sync") != 1 || strings.Count(notes, "RD: back in sync") != 1 {
		t.Fatalf("alerts: %s", notes)
	}
	// stale after 20 minutes without a report
	e.now = e.now.Add(30 * time.Minute)
	_, body = e.do("GET", "/api/admin/devices", tok, nil)
	if !strings.Contains(body, `"level":"stale"`) {
		t.Fatalf("not stale: %s", body)
	}
}
