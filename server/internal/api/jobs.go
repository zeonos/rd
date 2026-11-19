package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zeonos/rd/server/internal/store"
)

// Job delivery protocol (agent <-> server), all over HTTPS on /api/agent/*:
//
//	poll:   agent signs "rd-poll\n<id>\n<ts>" with its RustDesk device key;
//	        server returns due jobs, each as a JSON payload signed with the
//	        server's job key. The agent runs a job only if the signature
//	        verifies against the job key built into the client, the payload
//	        names this device, and it has not expired.
//	result: agent signs "rd-result\n<id>\n<job_id>\n<status>\n<exit_code>\n<sha256(output)>\n<ts>".
//
// The device key is recorded from the first sysinfo after enrolment.

const (
	jobKeyName      = "job-signing-key"
	agentClockSkew  = 5 * time.Minute
	maxOutputBytes  = 256 * 1024
	maxScriptBytes  = 64 * 1024
	agentPollPeriod = 10 // seconds, suggested to the agent
)

var powerActions = map[string]string{
	"reboot":           "Reboot",
	"reboot-safe-mode": "Reboot into Safe Mode with networking",
	"shutdown":         "Shut down",
	"logoff":           "Log off the console user",
}

// JobKey loads or creates the server's Ed25519 job signing key.
func JobKey(ctx context.Context, st store.Store) (ed25519.PrivateKey, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	v, err := st.PutServerKeyIfAbsent(ctx, jobKeyName, base64.StdEncoding.EncodeToString(seed))
	if err != nil {
		return nil, err
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) != ed25519.SeedSize {
		return nil, errors.New("stored job signing key is corrupt")
	}
	return ed25519.NewKeyFromSeed(b), nil
}

func (s *Server) jobKey(ctx context.Context) (ed25519.PrivateKey, error) {
	s.mu.Lock()
	k := s.signKey
	s.mu.Unlock()
	if k != nil {
		return k, nil
	}
	k, err := JobKey(ctx, s.store)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.signKey = k
	s.mu.Unlock()
	return k, nil
}

// --- agent side ---

func decodeKey(b64 string, size int) []byte {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) != size {
		return nil
	}
	return b
}

// verifyAgent checks a device signature and timestamp.
func (s *Server) verifyAgent(ctx context.Context, id, ts, sig, msg string) (*store.Device, error) {
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, errors.New("bad timestamp")
	}
	if d := s.now().Sub(time.Unix(t, 0)); d > agentClockSkew || d < -agentClockSkew {
		return nil, fmt.Errorf("clock skew %s", d.Round(time.Second))
	}
	dev, err := s.store.GetDevice(ctx, id)
	if err != nil {
		return nil, errors.New("unknown device")
	}
	pk := decodeKey(dev.PublicKey, ed25519.PublicKeySize)
	if pk == nil {
		return nil, errors.New("device key not registered yet")
	}
	sb := decodeKey(sig, ed25519.SignatureSize)
	if sb == nil || !ed25519.Verify(pk, []byte(msg), sb) {
		return nil, errors.New("bad signature")
	}
	return dev, nil
}

type agentPoll struct {
	ID  string `json:"id"`
	TS  string `json:"ts"`
	Sig string `json:"sig"`
}

// JobPayload is what the agent executes; it is signed as a whole.
type JobPayload struct {
	JobID      int64  `json:"job_id"`
	DeviceID   string `json:"device_id"`
	Kind       string `json:"kind"`
	Shell      string `json:"shell,omitempty"`
	Body       string `json:"body,omitempty"`
	RunAs      string `json:"run_as"`
	TimeoutSec int    `json:"timeout_sec"`
	Action     string `json:"action,omitempty"`
	IssuedAt   int64  `json:"issued_at"`
	ExpiresAt  int64  `json:"expires_at"` // agent must not start the job after this
}

type signedJob struct {
	Payload string `json:"payload"` // JSON of JobPayload, exactly the signed bytes
	Sig     string `json:"sig"`
}

func (s *Server) handleAgentPoll(w http.ResponseWriter, r *http.Request) {
	var req agentPoll
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	dev, err := s.verifyAgent(r.Context(), req.ID, req.TS, req.Sig, "rd-poll\n"+req.ID+"\n"+req.TS)
	if err != nil {
		s.cfg.Logger.Warn("agent poll rejected", "id", req.ID, "ip", s.clientIP(r), "err", err)
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	key, err := s.jobKey(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	now := s.now()
	jobs, err := s.store.ClaimRuns(r.Context(), dev.ID, now)
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]signedJob, 0, len(jobs))
	for _, j := range jobs {
		body := j.Body
		if j.Kind == "script" {
			body = Prelude(j.Shell, j.Vars) + body
		}
		p := JobPayload{JobID: j.ID, DeviceID: dev.ID, Kind: j.Kind, Shell: j.Shell, Body: body, RunAs: j.RunAs,
			TimeoutSec: j.TimeoutSec, Action: j.Action, IssuedAt: now.Unix(), ExpiresAt: j.Deadline.Unix()}
		b, _ := json.Marshal(p)
		out = append(out, signedJob{Payload: string(b), Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, b))})
		s.cfg.Logger.Info("job sent", "job", j.ID, "device", dev.ID, "kind", j.Kind)
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out, "poll_after": agentPollPeriod})
}

type agentResult struct {
	ID       string `json:"id"`
	TS       string `json:"ts"`
	JobID    int64  `json:"job_id"`
	Status   string `json:"status"` // success | failed
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
	Sig      string `json:"sig"`
}

func ResultMessage(id, ts string, jobID int64, status string, exitCode int, output string) string {
	sum := sha256.Sum256([]byte(output))
	return fmt.Sprintf("rd-result\n%s\n%d\n%s\n%d\n%s\n%s", id, jobID, status, exitCode, hex.EncodeToString(sum[:]), ts)
}

func (s *Server) handleAgentResult(w http.ResponseWriter, r *http.Request) {
	var req agentResult
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	if req.Status != store.RunSuccess && req.Status != store.RunFailed {
		writeError(w, http.StatusBadRequest, "status must be success or failed")
		return
	}
	msg := ResultMessage(req.ID, req.TS, req.JobID, req.Status, req.ExitCode, req.Output)
	if _, err := s.verifyAgent(r.Context(), req.ID, req.TS, req.Sig, msg); err != nil {
		s.cfg.Logger.Warn("agent result rejected", "id", req.ID, "ip", s.clientIP(r), "err", err)
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	out := req.Output
	if len(out) > maxOutputBytes {
		out = out[:maxOutputBytes] + "\n[output truncated]"
	}
	if err := s.store.FinishRun(r.Context(), req.JobID, req.ID, req.Status, req.ExitCode, out, s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusConflict, "No pending run for this job")
			return
		}
		s.internalError(w, err)
		return
	}
	s.cfg.Logger.Info("job result", "job", req.JobID, "device", req.ID, "status", req.Status, "exit", req.ExitCode)
	if req.Status == store.RunFailed {
		name := req.ID
		if d, err := s.store.GetDevice(r.Context(), req.ID); err == nil && (d.Alias != "" || d.Hostname != "") {
			name = d.Alias
			if name == "" {
				name = d.Hostname
			}
		}
		s.notify("RD: job failed", fmt.Sprintf("Job %d failed on %s (exit code %d)", req.JobID, name, req.ExitCode))
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) handleAgentKey(w http.ResponseWriter, r *http.Request) {
	k, err := s.jobKey(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey))})
}

// recordDeviceKey stores the device key on first sight (trust on first use
// after token enrolment). A different key later is ignored and logged.
func (s *Server) recordDeviceKey(ctx context.Context, d *store.Device, pk string, ip string) {
	if pk == "" || decodeKey(pk, ed25519.PublicKeySize) == nil || pk == d.PublicKey {
		return
	}
	if d.PublicKey != "" {
		s.cfg.Logger.Warn("device key change ignored", "id", d.ID, "ip", ip)
		return
	}
	if err := s.store.SetDevicePublicKey(ctx, d.ID, pk); err != nil {
		s.cfg.Logger.Error("set device key", "err", err)
		return
	}
	s.cfg.Logger.Info("device key registered", "id", d.ID)
}

// --- admin: scripts ---

func validateScript(sc *store.Script) error {
	sc.Name = strings.TrimSpace(sc.Name)
	if sc.Name == "" || len(sc.Name) > 200 {
		return errors.New("name is required")
	}
	if sc.Shell != "powershell" && sc.Shell != "cmd" {
		return errors.New("shell must be powershell or cmd")
	}
	if strings.TrimSpace(sc.Body) == "" || len(sc.Body) > maxScriptBytes {
		return errors.New("script body is empty or larger than 64 KB")
	}
	if sc.RunAs == "" {
		sc.RunAs = "system"
	}
	if sc.RunAs != "system" && sc.RunAs != "user" {
		return errors.New("run_as must be system or user")
	}
	if sc.TimeoutSec == 0 {
		sc.TimeoutSec = 300
	}
	if sc.TimeoutSec < 1 || sc.TimeoutSec > 86400 {
		return errors.New("timeout_sec must be 1-86400")
	}
	if len(sc.Description) > 1000 {
		return errors.New("description is longer than 1000 characters")
	}
	return validateParams(sc)
}

func (s *Server) adminListScripts(w http.ResponseWriter, r *http.Request) {
	sc, err := s.store.ListScripts(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	if sc == nil {
		sc = []store.Script{}
	}
	writeJSON(w, http.StatusOK, sc)
}

func (s *Server) adminSaveScript(w http.ResponseWriter, r *http.Request) {
	var sc store.Script
	if err := readJSON(r, &sc); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	sc.ID = 0
	if idStr := r.PathValue("id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Bad id")
			return
		}
		sc.ID = id
	}
	if err := validateScript(&sc); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch err := s.store.SaveScript(r.Context(), &sc); {
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "A script with that name already exists")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "Script not found")
	case err != nil:
		s.internalError(w, err)
	default:
		writeJSON(w, http.StatusOK, sc)
	}
}

func (s *Server) adminDeleteScript(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Bad id")
		return
	}
	if err := s.store.DeleteScript(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Script not found")
			return
		}
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- admin: jobs ---

type createJobRequest struct {
	// Either a saved script, an inline script, or a power action.
	ScriptID   int64  `json:"script_id"`
	Name       string `json:"name"`
	Shell      string `json:"shell"`
	Body       string `json:"body"`
	RunAs      string `json:"run_as"`
	TimeoutSec int    `json:"timeout_sec"`
	Action     string `json:"action"`

	// Targets: union of devices, groups (with subgroups) and all.
	Devices []string `json:"devices"`
	Groups  []int64  `json:"groups"`
	All     bool     `json:"all"`

	// Vars override device and location values for every device.
	Vars store.Vars `json:"vars"`

	StartInMinutes int `json:"start_in_minutes"` // 0 = now
	DeadlineHours  int `json:"deadline_hours"`   // default 24: offline devices run it when they return
}

type notFoundTarget struct{ msg string }

func (e notFoundTarget) Error() string { return e.msg }

func (s *Server) resolveTargets(ctx context.Context, req *createJobRequest) ([]string, string, error) {
	devices, err := s.store.ListDevices(ctx)
	if err != nil {
		return nil, "", err
	}
	selected := map[string]bool{}
	var parts []string
	if req.All {
		for _, d := range devices {
			selected[d.ID] = true
		}
		parts = append(parts, "all devices")
	}
	if len(req.Groups) > 0 {
		groups, err := s.store.ListGroups(ctx)
		if err != nil {
			return nil, "", err
		}
		byID := map[int64]store.Group{}
		for _, g := range groups {
			byID[g.ID] = g
		}
		want := map[int64]bool{}
		for _, gid := range req.Groups {
			g, ok := byID[gid]
			if !ok {
				return nil, "", notFoundTarget{fmt.Sprintf("Location %d not found", gid)}
			}
			parts = append(parts, g.Path)
			for _, x := range groups {
				if x.ID == g.ID || strings.HasPrefix(strings.ToLower(x.Path), strings.ToLower(g.Path)+"/") {
					want[x.ID] = true
				}
			}
		}
		for _, d := range devices {
			if d.GroupID != nil && want[*d.GroupID] {
				selected[d.ID] = true
			}
		}
	}
	if len(req.Devices) > 0 {
		known := map[string]string{}
		for _, d := range devices {
			name := d.Alias
			if name == "" {
				name = d.Hostname
			}
			if name == "" {
				name = d.ID
			}
			known[d.ID] = name
		}
		var names []string
		for _, id := range req.Devices {
			n, ok := known[id]
			if !ok {
				return nil, "", notFoundTarget{fmt.Sprintf("Device %s not found", id)}
			}
			selected[id] = true
			names = append(names, n)
		}
		if len(names) <= 3 {
			parts = append(parts, strings.Join(names, ", "))
		} else {
			parts = append(parts, fmt.Sprintf("%d devices", len(names)))
		}
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, strings.Join(parts, " + "), nil
}

// userError is a job request problem to show to the admin.
type userError struct{ msg string }

func (e userError) Error() string { return e.msg }

// jobPlan is a job ready to be stored, with the devices it runs on.
type jobPlan struct {
	job     *store.Job
	devices []string
	params  []store.ScriptParam
	// missing lists, per device, required variables without a value.
	missing map[string][]string
	vc      *varContext
}

// planJob turns a request into a job. Variables are resolved per device.
func (s *Server) planJob(ctx context.Context, req *createJobRequest, by string) (*jobPlan, error) {
	j := &store.Job{CreatedBy: by}
	var params []store.ScriptParam
	switch {
	case req.Action != "":
		label, ok := powerActions[req.Action]
		if !ok {
			return nil, userError{"action must be reboot, reboot-safe-mode, shutdown or logoff"}
		}
		j.Kind, j.Name, j.Action, j.RunAs, j.TimeoutSec = "power", label, req.Action, "system", 60
	case req.ScriptID != 0:
		sc, err := s.store.GetScript(ctx, req.ScriptID)
		if err != nil {
			return nil, userError{"Script not found"}
		}
		id := sc.ID
		j.Kind, j.Name, j.Shell, j.Body, j.RunAs, j.TimeoutSec, j.ScriptID = "script", sc.Name, sc.Shell, sc.Body, sc.RunAs, sc.TimeoutSec, &id
		params = sc.Params
	default:
		sc := store.Script{Name: req.Name, Shell: req.Shell, Body: req.Body, RunAs: req.RunAs, TimeoutSec: req.TimeoutSec}
		if sc.Name == "" {
			sc.Name = "Ad-hoc script"
		}
		if err := validateScript(&sc); err != nil {
			return nil, userError{err.Error()}
		}
		j.Kind, j.Name, j.Shell, j.Body, j.RunAs, j.TimeoutSec = "script", sc.Name, sc.Shell, sc.Body, sc.RunAs, sc.TimeoutSec
	}
	req.Vars = cleanVars(req.Vars)
	if err := checkVars(req.Vars); err != nil {
		return nil, userError{err.Error()}
	}
	ids, target, err := s.resolveTargets(ctx, req)
	if err != nil {
		var nf notFoundTarget
		if errors.As(err, &nf) {
			return nil, userError{err.Error()}
		}
		return nil, err
	}
	if len(ids) == 0 {
		return nil, userError{"No devices selected"}
	}
	if req.StartInMinutes < 0 || req.StartInMinutes > 60*24*30 || req.DeadlineHours < 0 || req.DeadlineHours > 24*30 {
		return nil, userError{"Bad schedule"}
	}
	if req.DeadlineHours == 0 {
		req.DeadlineHours = 24
	}
	now := s.now()
	j.Target = target
	j.NotBefore = now.Add(time.Duration(req.StartInMinutes) * time.Minute)
	j.Deadline = j.NotBefore.Add(time.Duration(req.DeadlineHours) * time.Hour)
	plan := &jobPlan{job: j, devices: ids, params: params, missing: map[string][]string{}}
	if j.Kind == "script" {
		vc, err := s.loadVarContext(ctx)
		if err != nil {
			return nil, err
		}
		plan.vc = vc
		j.RunVars = map[string]store.Vars{}
		for _, id := range ids {
			v, miss := vc.resolve(id, params, req.Vars)
			j.RunVars[id] = v
			if len(miss) > 0 {
				plan.missing[id] = miss
			}
		}
	}
	return plan, nil
}

// missingText describes devices that lack required variables.
func (p *jobPlan) missingText() string {
	var parts []string
	for _, id := range p.devices {
		if m := p.missing[id]; len(m) > 0 {
			parts = append(parts, fmt.Sprintf("%s (%s)", displayName(p.vc.devices[id]), strings.Join(m, ", ")))
		}
	}
	if len(parts) > 5 {
		parts = append(parts[:5], fmt.Sprintf("and %d more", len(parts)-5))
	}
	return strings.Join(parts, "; ")
}

func (s *Server) adminCreateJob(w http.ResponseWriter, r *http.Request) {
	var req createJobRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	plan, err := s.planJob(r.Context(), &req, userFrom(r).Name)
	var ue userError
	if errors.As(err, &ue) {
		writeError(w, http.StatusBadRequest, ue.msg)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if len(plan.missing) > 0 {
		writeError(w, http.StatusBadRequest, "Variables without a value on: "+plan.missingText()+
			". Set them under Variables, or type a value for every device.")
		return
	}
	j := plan.job
	if err := s.store.CreateJob(r.Context(), j, plan.devices); err != nil {
		s.internalError(w, err)
		return
	}
	s.cfg.Logger.Info("job created", "job", j.ID, "kind", j.Kind, "name", j.Name, "devices", len(plan.devices), "by", j.CreatedBy)
	writeJSON(w, http.StatusOK, map[string]any{"job": j, "devices": len(plan.devices)})
}

// adminPreviewJob shows the devices a job would run on and their variables.
func (s *Server) adminPreviewJob(w http.ResponseWriter, r *http.Request) {
	var req createJobRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	plan, err := s.planJob(r.Context(), &req, userFrom(r).Name)
	var ue userError
	if errors.As(err, &ue) {
		writeError(w, http.StatusBadRequest, ue.msg)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	type row struct {
		ID      string     `json:"id"`
		Name    string     `json:"name"`
		Vars    store.Vars `json:"vars"`
		Missing []string   `json:"missing"`
	}
	rows := []row{}
	for _, id := range plan.devices {
		name := id
		if plan.vc != nil {
			name = displayName(plan.vc.devices[id])
		}
		rows = append(rows, row{ID: id, Name: name, Vars: plan.job.RunVars[id], Missing: plan.missing[id]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": plan.job.Target, "params": plan.params, "devices": rows})
}

func (s *Server) adminListJobs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := r.URL.Query()
	scriptID, _ := strconv.ParseInt(q.Get("script_id"), 10, 64)
	scheduleID, _ := strconv.ParseInt(q.Get("schedule_id"), 10, 64)
	jobs, err := s.store.ListJobs(r.Context(), store.JobFilter{Limit: limit, ScriptID: scriptID, ScheduleID: scheduleID})
	if err != nil {
		s.internalError(w, err)
		return
	}
	if jobs == nil {
		jobs = []store.Job{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) adminGetJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Bad id")
		return
	}
	j, runs, err := s.store.GetJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Job not found")
			return
		}
		s.internalError(w, err)
		return
	}
	if runs == nil {
		runs = []store.JobRun{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": j, "runs": runs})
}

func (s *Server) adminCancelJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Bad id")
		return
	}
	if err := s.store.CancelJob(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Job not found")
			return
		}
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
