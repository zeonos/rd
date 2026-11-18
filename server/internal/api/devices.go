package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/zeonos/rd/server/internal/auth"
	"github.com/zeonos/rd/server/internal/store"
)

// --- device side ---

type heartbeatRequest struct {
	ID         string `json:"id"`
	UUID       string `json:"uuid"`
	Ver        int64  `json:"ver"`
	Conns      []int  `json:"conns"`
	ModifiedAt int64  `json:"modified_at"`
}

// handleHeartbeat records presence. Unknown or mismatched devices get an empty
// answer: they must enrol first (rd --assign --token ...).
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req heartbeatRequest
	if err := readJSON(r, &req); err != nil || req.ID == "" {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	d, err := s.store.GetDevice(r.Context(), req.ID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.cfg.Logger.Error("heartbeat", "err", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if d.UUID != req.UUID {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if err := s.store.Heartbeat(r.Context(), req.ID, len(req.Conns), s.now()); err != nil {
		s.cfg.Logger.Error("heartbeat", "err", err)
	}
	resp := map[string]any{}
	if d.Hostname == "" {
		resp["sysinfo"] = true // never reported: ask for it once
	}
	writeJSON(w, http.StatusOK, resp)
}

type sysinfoRequest struct {
	ID       string `json:"id"`
	UUID     string `json:"uuid"`
	Hostname string `json:"hostname"`
	Username string `json:"username"`
	OS       string `json:"os"`
	CPU      string `json:"cpu"`
	Memory   string `json:"memory"`
	Version  string `json:"version"`
	// Added by the RD client patch: base64 h1 of the unattended password.
	PasswordH1 string `json:"rd_pw_h1"`
}

// handleSysinfo answers in plain text like RustDesk Server Pro:
// "SYSINFO_UPDATED" on success. Anything else makes the client retry later.
func (s *Server) handleSysinfo(w http.ResponseWriter, r *http.Request) {
	var req sysinfoRequest
	if err := readJSON(r, &req); err != nil || req.ID == "" {
		writeText(w, http.StatusOK, "BAD_REQUEST")
		return
	}
	d, err := s.store.GetDevice(r.Context(), req.ID)
	if err != nil {
		writeText(w, http.StatusOK, "NOT_ENROLLED")
		return
	}
	if d.UUID != req.UUID {
		s.cfg.Logger.Warn("sysinfo uuid mismatch", "id", req.ID, "ip", s.clientIP(r))
		writeText(w, http.StatusOK, "UUID_MISMATCH")
		return
	}
	h1 := req.PasswordH1
	if h1 != "" {
		if b, err := base64.StdEncoding.DecodeString(h1); err != nil || len(b) != 32 {
			h1 = "" // ignore malformed values
		}
	}
	err = s.store.UpdateSysinfo(r.Context(), req.ID, store.SysinfoUpdate{
		Hostname: req.Hostname, Username: req.Username, OS: req.OS, CPU: req.CPU,
		Memory: req.Memory, Version: req.Version, PasswordH1: h1,
	})
	if err != nil {
		s.cfg.Logger.Error("sysinfo", "err", err)
		writeText(w, http.StatusOK, "ERROR")
		return
	}
	writeText(w, http.StatusOK, "SYSINFO_UPDATED")
}

type enrollRequest struct {
	ID              string `json:"id"`
	UUID            string `json:"uuid"`
	DeviceGroupName string `json:"device_group_name"`
	DeviceName      string `json:"device_name"`
	Note            string `json:"note"`
}

// handleEnroll serves `rd --assign --token <enrolment token> --device_group_name <path>`.
// The response body is printed by the CLI; empty means "Done!".
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	tok := bearer(r)
	var req enrollRequest
	if err := readJSON(r, &req); err != nil || req.ID == "" || req.UUID == "" {
		writeText(w, http.StatusBadRequest, "Bad request: id and uuid are required")
		return
	}
	if tok == "" {
		writeText(w, http.StatusUnauthorized, "Enrolment token required (--token)")
		return
	}
	now := s.now()
	ip := s.clientIP(r)
	if locked, _ := s.ipLimit.Locked("enroll:"+ip, now); locked {
		writeText(w, http.StatusTooManyRequests, msgLocked)
		return
	}
	t, err := s.store.UseEnrollToken(r.Context(), auth.HashToken(tok), now)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.ipLimit.Fail("enroll:"+ip, now)
		writeText(w, http.StatusUnauthorized, "Invalid enrolment token")
		return
	case errors.Is(err, store.ErrTokenUnusable):
		writeText(w, http.StatusUnauthorized, "Enrolment token expired or used up")
		return
	case err != nil:
		s.cfg.Logger.Error("enroll", "err", err)
		writeText(w, http.StatusInternalServerError, "Internal error")
		return
	}
	groupID := t.GroupID
	groupPath := t.GroupPath
	if groupID == nil && strings.TrimSpace(req.DeviceGroupName) != "" {
		g, err := s.store.EnsureGroupPath(r.Context(), req.DeviceGroupName)
		if err != nil {
			s.cfg.Logger.Error("enroll group", "err", err)
			writeText(w, http.StatusInternalServerError, "Internal error")
			return
		}
		groupID, groupPath = &g.ID, g.Path
	}
	d := &store.Device{ID: req.ID, UUID: req.UUID, GroupID: groupID, Alias: req.DeviceName, Note: req.Note}
	if err := s.store.EnrollDevice(r.Context(), d); err != nil {
		s.cfg.Logger.Error("enroll", "err", err)
		writeText(w, http.StatusInternalServerError, "Internal error")
		return
	}
	s.cfg.Logger.Info("device enrolled", "id", req.ID, "group", groupPath, "ip", ip)
	s.notify("RD device enrolled", fmt.Sprintf("Device %s enrolled in %q from %s", req.ID, groupPath, ip))
	writeText(w, http.StatusOK, "")
}

// --- viewer side: address book and device groups ---

func platformOf(os string) string {
	l := strings.ToLower(os)
	switch {
	case strings.Contains(l, "windows"):
		return "Windows"
	case strings.Contains(l, "mac"):
		return "Mac OS"
	case strings.Contains(l, "android"):
		return "Android"
	case strings.Contains(l, "linux"):
		return "Linux"
	}
	return ""
}

const unassigned = "Unassigned"

func groupName(d *store.Device) string {
	if d.GroupPath == "" {
		return unassigned
	}
	return d.GroupPath
}

type abPeer struct {
	ID              string   `json:"id"`
	Hash            string   `json:"hash"`
	Username        string   `json:"username"`
	Hostname        string   `json:"hostname"`
	Platform        string   `json:"platform"`
	Alias           string   `json:"alias"`
	Tags            []string `json:"tags"`
	Note            string   `json:"note"`
	DeviceGroupName string   `json:"device_group_name"`
}

// handleGetAB returns the legacy address book: every device, tagged with its
// location, carrying the unattended-access hash so the viewer connects
// without a password prompt. Edits made in the client are not stored; groups
// and names are managed on the server.
func (s *Server) handleGetAB(w http.ResponseWriter, r *http.Request) {
	devices, err := s.store.ListDevices(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	groups, err := s.store.ListGroups(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	tags := make([]string, 0, len(groups)+1)
	for _, g := range groups {
		tags = append(tags, g.Path)
	}
	peers := make([]abPeer, 0, len(devices))
	hasUnassigned := false
	for i := range devices {
		d := &devices[i]
		g := groupName(d)
		if g == unassigned {
			hasUnassigned = true
		}
		peers = append(peers, abPeer{
			ID: d.ID, Hash: d.PasswordH1, Username: d.Username, Hostname: d.Hostname,
			Platform: platformOf(d.OS), Alias: d.Alias, Tags: []string{g}, Note: d.Note,
			DeviceGroupName: g,
		})
	}
	if hasUnassigned {
		tags = append(tags, unassigned)
	}
	data, _ := json.Marshal(map[string]any{"tags": tags, "peers": peers, "tag_colors": "{}"})
	writeJSON(w, http.StatusOK, map[string]any{"data": string(data), "licensed_devices": 100000})
}

func (s *Server) handlePostAB(w http.ResponseWriter, r *http.Request) {
	// Accept and ignore: the server is the source of truth.
	w.WriteHeader(http.StatusOK)
}

type paged struct {
	Total int `json:"total"`
	Data  any `json:"data"`
}

func (s *Server) handleDeviceGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListGroups(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]map[string]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, map[string]string{"name": g.Path})
	}
	writeJSON(w, http.StatusOK, paged{Total: len(out), Data: out})
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	writeJSON(w, http.StatusOK, paged{Total: 1, Data: []userPayload{toPayload(u)}})
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	devices, err := s.store.ListDevices(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	sort.Slice(devices, func(i, j int) bool {
		gi, gj := groupName(&devices[i]), groupName(&devices[j])
		if gi != gj {
			return gi < gj
		}
		return devices[i].Hostname < devices[j].Hostname
	})
	out := make([]map[string]any, 0, len(devices))
	for i := range devices {
		d := &devices[i]
		name := d.Hostname
		if d.Alias != "" {
			name = d.Alias
		}
		out = append(out, map[string]any{
			"id":                d.ID,
			"info":              map[string]string{"username": d.Username, "os": d.OS, "device_name": name},
			"status":            1,
			"user":              "",
			"user_name":         "",
			"device_group_name": groupName(d),
			"note":              d.Note,
		})
	}
	writeJSON(w, http.StatusOK, paged{Total: len(out), Data: out})
}
