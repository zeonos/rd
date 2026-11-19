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

// agent registers a device key and returns a poll function for it.
func (e *env) agent(id, host string) func() []JobPayload {
	e.t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	e.do("POST", "/api/sysinfo", "", map[string]any{"id": id, "uuid": "u" + id, "hostname": host,
		"rd_pk": base64.StdEncoding.EncodeToString(pub)})
	return func() []JobPayload {
		e.t.Helper()
		ts := strconv.FormatInt(e.now.Unix(), 10)
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte("rd-poll\n"+id+"\n"+ts)))
		code, body := e.do("POST", "/api/agent/poll", "", map[string]any{"id": id, "ts": ts, "sig": sig})
		if code != 200 {
			e.t.Fatalf("poll %s: %d %s", id, code, body)
		}
		var poll struct {
			Jobs []signedJob `json:"jobs"`
		}
		json.Unmarshal([]byte(body), &poll)
		var out []JobPayload
		for _, j := range poll.Jobs {
			var p JobPayload
			json.Unmarshal([]byte(j.Payload), &p)
			out = append(out, p)
		}
		return out
	}
}

func TestPrelude(t *testing.T) {
	ps := Prelude("powershell", store.Vars{"IP": "10.0.0.1", "NOTE": "it's"})
	if ps != "$IP = '10.0.0.1'\r\n$NOTE = 'it''s'\r\n" {
		t.Fatalf("powershell: %q", ps)
	}
	cmd := Prelude("cmd", store.Vars{"P": `100% "x" & y`})
	if cmd != "@set \"P=100%% \"x\" & y\"\r\n" {
		t.Fatalf("cmd: %q", cmd)
	}
	if Prelude("powershell", nil) != "" {
		t.Fatal("empty prelude")
	}
}

func TestScriptVariables(t *testing.T) {
	e := newEnv(t)
	tok := e.login()
	ctx := context.Background()
	site, _ := e.st.EnsureGroupPath(ctx, "Kunder/Baune")
	parent, _ := e.st.EnsureGroupPath(ctx, "Kunder")
	e.st.EnrollDevice(ctx, &store.Device{ID: "1", UUID: "u1", GroupID: &site.ID})
	e.st.EnrollDevice(ctx, &store.Device{ID: "2", UUID: "u2", GroupID: &site.ID})
	poll1 := e.agent("1", "SRV-BAUNE")
	poll2 := e.agent("2", "SRV-BAUNE2")

	// bad names refused
	if code, _ := e.do("PUT", "/api/admin/vars", tok, map[string]any{"devices": map[string]any{"1": map[string]string{"Host": "x"}}}); code != 400 {
		t.Fatal("reserved name accepted")
	}
	if code, _ := e.do("PUT", "/api/admin/vars", tok, map[string]any{"devices": map[string]any{"1": map[string]string{"A": "x\ny"}}}); code != 400 {
		t.Fatal("multi-line value accepted")
	}
	// location value inherited; device value wins
	code, body := e.do("PUT", "/api/admin/vars", tok, map[string]any{
		"groups":  map[string]any{strconv.FormatInt(parent.ID, 10): map[string]string{"PRIMARY": "srv-b1903", "PORT": "8384"}},
		"devices": map[string]any{"1": map[string]string{"IP": "10.0.0.1", "PORT": "9000"}},
	})
	if code != 204 {
		t.Fatalf("set vars: %d %s", code, body)
	}

	code, body = e.do("POST", "/api/admin/scripts", tok, map[string]any{"name": "Ping", "shell": "powershell", "body": "Test-Connection $IP",
		"params": []map[string]string{{"name": "IP", "description": "Address"}, {"name": "COUNT", "default": "4"}}})
	if code != 200 {
		t.Fatalf("save script: %d %s", code, body)
	}
	sid := int64(decode(t, body)["id"].(float64))
	if code, _ := e.do("POST", "/api/admin/scripts", tok, map[string]any{"name": "Bad", "shell": "cmd", "body": "x",
		"params": []map[string]string{{"name": "RD_X"}}}); code != 400 {
		t.Fatal("RD_ param accepted")
	}

	// device 2 has no IP: the job is refused and says where
	code, body = e.do("POST", "/api/admin/jobs", tok, map[string]any{"script_id": sid, "groups": []int64{site.ID}})
	if code != 400 || !strings.Contains(body, "SRV-BAUNE2 (IP)") {
		t.Fatalf("missing variable: %d %s", code, body)
	}
	// preview shows values and what is missing
	code, body = e.do("POST", "/api/admin/jobs/preview", tok, map[string]any{"script_id": sid, "groups": []int64{site.ID}})
	if code != 200 || !strings.Contains(body, `"missing":["IP"]`) || !strings.Contains(body, `"PRIMARY":"srv-b1903"`) {
		t.Fatalf("preview: %d %s", code, body)
	}
	// set it for device 2 and run
	e.do("PUT", "/api/admin/vars", tok, map[string]any{"devices": map[string]any{"2": map[string]string{"IP": "10.0.0.2"}}})
	code, body = e.do("POST", "/api/admin/jobs", tok, map[string]any{"script_id": sid, "groups": []int64{site.ID}})
	if code != 200 {
		t.Fatalf("job: %d %s", code, body)
	}
	jobID := int64(decode(t, body)["job"].(map[string]any)["id"].(float64))

	p1 := poll1()
	if len(p1) != 1 {
		t.Fatalf("poll 1: %+v", p1)
	}
	for _, want := range []string{"$IP = '10.0.0.1'", "$PORT = '9000'", "$PRIMARY = 'srv-b1903'", "$COUNT = '4'",
		"$RD_HOSTNAME = 'SRV-BAUNE'", "$RD_LOCATION = 'Kunder/Baune'", "$RD_DEVICE_ID = '1'"} {
		if !strings.Contains(p1[0].Body, want+"\r\n") {
			t.Fatalf("body lacks %q:\n%s", want, p1[0].Body)
		}
	}
	if !strings.HasSuffix(p1[0].Body, "Test-Connection $IP") {
		t.Fatalf("script body not last: %q", p1[0].Body)
	}
	if p2 := poll2(); len(p2) != 1 || !strings.Contains(p2[0].Body, "$IP = '10.0.0.2'") || !strings.Contains(p2[0].Body, "$PORT = '8384'") {
		t.Fatalf("poll 2: %+v", p2)
	}

	// a value typed when starting overrides everything
	code, body = e.do("POST", "/api/admin/jobs", tok, map[string]any{"script_id": sid, "devices": []string{"1"}, "vars": map[string]string{"IP": "8.8.8.8"}})
	if code != 200 {
		t.Fatalf("override: %d %s", code, body)
	}
	if p := poll1(); len(p) != 1 || !strings.Contains(p[0].Body, "$IP = '8.8.8.8'") {
		t.Fatalf("override not applied: %+v", p)
	}

	// run log: the job keeps what each run got; history per script and device
	_, body = e.do("GET", "/api/admin/jobs/"+strconv.FormatInt(jobID, 10), tok, nil)
	if !strings.Contains(body, `"IP":"10.0.0.2"`) {
		t.Fatalf("run vars not recorded: %s", body)
	}
	_, body = e.do("GET", "/api/admin/jobs?script_id="+strconv.FormatInt(sid, 10), tok, nil)
	var jobs []store.Job
	json.Unmarshal([]byte(body), &jobs)
	if len(jobs) != 2 {
		t.Fatalf("jobs for script: %s", body)
	}
	_, body = e.do("GET", "/api/admin/devices/1/runs", tok, nil)
	var runs []store.DeviceRun
	json.Unmarshal([]byte(body), &runs)
	if len(runs) != 2 || runs[0].JobName != "Ping" || runs[0].Status != store.RunSent {
		t.Fatalf("device runs: %s", body)
	}
}

func TestScriptImportExport(t *testing.T) {
	e := newEnv(t)
	tok := e.login()
	e.do("POST", "/api/admin/scripts", tok, map[string]any{"name": "One", "shell": "powershell", "body": "1",
		"params": []map[string]string{{"name": "SERVER", "default": "a"}}})
	e.do("POST", "/api/admin/scripts", tok, map[string]any{"name": "Two", "shell": "cmd", "body": "dir"})

	code, body := e.do("GET", "/api/admin/scripts/export", tok, nil)
	if code != 200 {
		t.Fatalf("export: %d %s", code, body)
	}
	var b scriptBundle
	json.Unmarshal([]byte(body), &b)
	if b.Format != "rd-scripts" || len(b.Scripts) != 2 || b.Scripts[0].Params[0].Name != "SERVER" {
		t.Fatalf("bundle: %s", body)
	}

	// import into a fresh server
	f := newEnv(t)
	ftok := f.login()
	b.Scripts[1].Body = "dir /s"
	code, body = f.do("POST", "/api/admin/scripts/import", ftok, b)
	if code != 200 || decode(t, body)["created"].(float64) != 2 {
		t.Fatalf("import: %d %s", code, body)
	}
	// again: skipped unless replace
	_, body = f.do("POST", "/api/admin/scripts/import", ftok, b)
	if m := decode(t, body); m["created"].(float64) != 0 || len(m["skipped"].([]any)) != 2 {
		t.Fatalf("re-import: %s", body)
	}
	b.Scripts[0].Body = "2"
	req := map[string]any{"format": b.Format, "version": 1, "scripts": b.Scripts, "replace": true}
	_, body = f.do("POST", "/api/admin/scripts/import", ftok, req)
	if decode(t, body)["updated"].(float64) != 2 {
		t.Fatalf("replace: %s", body)
	}
	list, _ := f.st.ListScripts(context.Background())
	if len(list) != 2 || list[0].Body != "2" || list[0].Params[0].Default != "a" {
		t.Fatalf("after import: %+v", list)
	}
	if code, _ := f.do("POST", "/api/admin/scripts/import", ftok, map[string]any{"scripts": []any{}}); code != 400 {
		t.Fatal("non-export accepted")
	}
}

func TestNextRun(t *testing.T) {
	cph := "Europe/Copenhagen"
	// 2026-11-19 is a Thursday. 06:30 local = 05:30 UTC (CET).
	after := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	sc := &store.Schedule{Kind: "days", AtMinute: 6*60 + 30, Weekdays: 0x7f, TZ: cph}
	if got := nextRun(sc, after); !got.Equal(time.Date(2026, 11, 20, 5, 30, 0, 0, time.UTC)) {
		t.Fatalf("daily: %s", got)
	}
	sc.Weekdays = 1 << 1 // Mondays
	if got := nextRun(sc, after); !got.Equal(time.Date(2026, 11, 23, 5, 30, 0, 0, time.UTC)) {
		t.Fatalf("monday: %s", got)
	}
	h := &store.Schedule{Kind: "hours", EveryHours: 6}
	if got := nextRun(h, after); !got.Equal(after.Add(6 * time.Hour)) {
		t.Fatalf("hours first: %s", got)
	}
	h.NextRun = after.Add(-13 * time.Hour) // missed two runs while down
	if got := nextRun(h, after); !got.Equal(after.Add(5 * time.Hour)) {
		t.Fatalf("hours catch-up: %s", got)
	}
}

func TestSchedules(t *testing.T) {
	e := newEnv(t)
	tok := e.login()
	ctx := context.Background()
	g, _ := e.st.EnsureGroupPath(ctx, "Sites")
	e.st.EnrollDevice(ctx, &store.Device{ID: "7", UUID: "u7", GroupID: &g.ID})
	e.st.EnrollDevice(ctx, &store.Device{ID: "8", UUID: "u8", GroupID: &g.ID})
	poll7 := e.agent("7", "SEVEN")
	e.st.SetDeviceVars(ctx, "7", store.Vars{"IP": "1.2.3.4"})
	_, body := e.do("POST", "/api/admin/scripts", tok, map[string]any{"name": "Sync check", "shell": "powershell", "body": "check $IP",
		"params": []map[string]string{{"name": "IP"}}})
	sid := decode(t, body)["id"]

	if code, _ := e.do("POST", "/api/admin/schedules", tok, map[string]any{"script_id": sid, "kind": "days", "weekdays": 0, "target": map[string]any{"all": true}}); code != 400 {
		t.Fatal("no weekdays accepted")
	}
	if code, _ := e.do("POST", "/api/admin/schedules", tok, map[string]any{"script_id": sid, "kind": "hours", "every_hours": 2, "tz": "Mars/Base", "target": map[string]any{"all": true}}); code != 400 {
		t.Fatal("bad time zone accepted")
	}
	code, body := e.do("POST", "/api/admin/schedules", tok, map[string]any{"script_id": sid, "kind": "hours", "every_hours": 2,
		"tz": "Europe/Copenhagen", "target": map[string]any{"groups": []int64{g.ID}}})
	if code != 200 {
		t.Fatalf("create schedule: %d %s", code, body)
	}
	sch := decode(t, body)
	if sch["target_text"] != "Sites" || !sch["enabled"].(bool) {
		t.Fatalf("schedule: %s", body)
	}
	schID := strconv.FormatInt(int64(sch["id"].(float64)), 10)

	// not due yet
	e.srv.runSchedules(ctx, e.now)
	if p := poll7(); len(p) != 0 {
		t.Fatal("ran early")
	}
	// due: device 7 runs it; 8 is skipped (no IP) and that is reported
	e.now = e.now.Add(2*time.Hour + time.Minute)
	e.srv.runSchedules(ctx, e.now)
	p := poll7()
	if len(p) != 1 || !strings.Contains(p[0].Body, "$IP = '1.2.3.4'") {
		t.Fatalf("scheduled job: %+v", p)
	}
	var notes string
	for i := 0; i < 100 && !strings.Contains(notes, "RD: scheduled script"); i++ {
		time.Sleep(5 * time.Millisecond) // notifications are sent asynchronously
		e.mu.Lock()
		notes = strings.Join(e.notes, "|")
		e.mu.Unlock()
	}
	if !strings.Contains(notes, "RD: scheduled script") {
		t.Fatalf("skip not reported: %s", notes)
	}
	// only once per due time
	e.srv.runSchedules(ctx, e.now)
	_, body = e.do("GET", "/api/admin/jobs?schedule_id="+schID, tok, nil)
	var jobs []store.Job
	json.Unmarshal([]byte(body), &jobs)
	if len(jobs) != 1 || jobs[0].Counts[store.RunSent] != 1 {
		t.Fatalf("jobs for schedule: %s", body)
	}
	got, _ := e.st.GetSchedule(ctx, int64(sch["id"].(float64)))
	if !got.NextRun.After(e.now) || got.LastJobID == nil {
		t.Fatalf("schedule not advanced: %+v", got)
	}
	// run now, then pause
	if code, body := e.do("POST", "/api/admin/schedules/"+schID+"/run", tok, nil); code != 200 {
		t.Fatalf("run now: %d %s", code, body)
	}
	code, body = e.do("PUT", "/api/admin/schedules/"+schID, tok, map[string]any{"script_id": sid, "kind": "hours", "every_hours": 2,
		"tz": "Europe/Copenhagen", "target": map[string]any{"groups": []int64{g.ID}}, "enabled": false})
	if code != 200 || decode(t, body)["enabled"].(bool) {
		t.Fatalf("pause: %d %s", code, body)
	}
	e.now = e.now.Add(5 * time.Hour)
	e.srv.runSchedules(ctx, e.now)
	_, body = e.do("GET", "/api/admin/jobs?schedule_id="+schID, tok, nil)
	json.Unmarshal([]byte(body), &jobs)
	if len(jobs) != 2 {
		t.Fatalf("paused schedule ran: %s", body)
	}
	if code, _ := e.do("DELETE", "/api/admin/schedules/"+schID, tok, nil); code != 204 {
		t.Fatal("delete schedule")
	}
}
