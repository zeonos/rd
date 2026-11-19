package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/zeonos/rd/server/internal/store"
)

// Syncthing status
//
// The agent runs syncthing.ps1 every few minutes: it finds the local
// Syncthing config, asks its REST API for each folder's state and how far
// each remote device has come, and posts the result to /api/agent/sync,
// signed "rd-sync\n<id>\n<sha256(report)>\n<ts>" with the device key.
// The console shows a summary per device; an alert goes out when a device
// stays out of sync longer than SyncAlert.

const (
	maxSyncBytes = 128 * 1024
	// syncStale: a report older than this is not trusted (agent reports every 5 min).
	syncStale = 20 * time.Minute
)

func SyncMessage(id, ts, report string) string {
	sum := sha256.Sum256([]byte(report))
	return fmt.Sprintf("rd-sync\n%s\n%s\n%s", id, hex.EncodeToString(sum[:]), ts)
}

// SyncReport is what syncthing.ps1 prints.
type SyncReport struct {
	Found   bool         `json:"found"`
	Error   string       `json:"error"`
	Version string       `json:"version"`
	MyID    string       `json:"my_id"`
	MyName  string       `json:"my_name"`
	Folders []SyncFolder `json:"folders"`
	Devices []SyncDevice `json:"devices"`
}

type SyncFolder struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Path        string     `json:"path"`
	Type        string     `json:"type"` // sendreceive | sendonly | receiveonly | receiveencrypted
	State       string     `json:"state"`
	Paused      bool       `json:"paused"`
	NeedBytes   int64      `json:"need_bytes"`
	NeedItems   int64      `json:"need_items"`
	GlobalBytes int64      `json:"global_bytes"`
	InSyncBytes int64      `json:"in_sync_bytes"`
	Errors      int        `json:"errors"`
	Peers       []SyncPeer `json:"devices"`
}

// SyncPeer is a remote device sharing a folder, as seen from this device.
type SyncPeer struct {
	ID          string  `json:"id"`
	Completion  float64 `json:"completion"`
	NeedBytes   int64   `json:"need_bytes"`
	RemoteState string  `json:"remote_state"` // valid | paused | notSharing | unknown
}

type SyncDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Paused    bool   `json:"paused"`
	LastSeen  string `json:"last_seen"`
}

// SyncSummary is the one-line status in the device list.
type SyncSummary struct {
	// Level: ok | syncing | warn | error | none (not installed) | stale
	Level  string    `json:"level"`
	Text   string    `json:"text"`
	Detail []string  `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

func shortID(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	return id
}

func pct(done, total int64) int {
	if total <= 0 {
		return 100
	}
	return int(math.Floor(float64(done) * 100 / float64(total)))
}

// Summarize turns a report into a status. Problems are listed worst first.
func Summarize(rep *SyncReport) (level, text string, detail []string) {
	if !rep.Found {
		return "none", "Not installed", nil
	}
	if rep.Error != "" {
		return "error", "Syncthing not reachable", []string{rep.Error}
	}
	names := map[string]string{}
	conn := map[string]SyncDevice{}
	for _, d := range rep.Devices {
		names[d.ID] = d.Name
		conn[d.ID] = d
	}
	name := func(id string) string {
		if n := names[id]; n != "" {
			return n
		}
		return shortID(id)
	}
	folderName := func(f SyncFolder) string {
		if f.Label != "" {
			return f.Label
		}
		return f.ID
	}
	var errs, warns, syncing []string
	// remote devices behind, by device: worst completion over shared folders
	behind := map[string]float64{}
	for _, f := range rep.Folders {
		if f.Paused {
			warns = append(warns, fmt.Sprintf("Folder %s is paused", folderName(f)))
			continue
		}
		switch {
		case f.Errors > 0:
			errs = append(errs, fmt.Sprintf("Folder %s: %d item(s) failed to sync", folderName(f), f.Errors))
		case strings.Contains(f.State, "error"):
			errs = append(errs, fmt.Sprintf("Folder %s: %s", folderName(f), f.State))
		case f.NeedBytes > 0 || f.NeedItems > 0 || strings.HasPrefix(f.State, "sync"):
			p := pct(f.InSyncBytes, f.GlobalBytes)
			if p >= 100 {
				p = 99
			}
			syncing = append(syncing, fmt.Sprintf("%s: %d%%, %s to go", folderName(f), p, humanBytes(f.NeedBytes)))
		}
		for _, peer := range f.Peers {
			if peer.ID == rep.MyID || peer.RemoteState == "notSharing" || peer.RemoteState == "paused" {
				continue
			}
			if d, ok := conn[peer.ID]; ok && d.Paused {
				continue
			}
			if peer.Completion < 100 {
				if cur, ok := behind[peer.ID]; !ok || peer.Completion < cur {
					behind[peer.ID] = peer.Completion
				}
			}
		}
	}
	ids := make([]string, 0, len(behind))
	for id := range behind {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return behind[ids[i]] < behind[ids[j]] })
	for _, id := range ids {
		d, known := conn[id]
		c := int(math.Floor(behind[id]))
		if known && !d.Connected {
			seen := ""
			if d.LastSeen != "" {
				if t, err := time.Parse(time.RFC3339, d.LastSeen); err == nil && t.Year() > 1970 {
					seen = ", last seen " + t.UTC().Format("2006-01-02 15:04") + " UTC"
				}
			}
			warns = append(warns, fmt.Sprintf("%s is not connected (%d%%%s)", name(id), c, seen))
			continue
		}
		syncing = append(syncing, fmt.Sprintf("%s %d%%", name(id), c))
	}
	detail = append(append(append(detail, errs...), warns...), syncing...)
	more := func(first string) string {
		if len(detail) > 1 {
			return fmt.Sprintf("%s (+%d more)", first, len(detail)-1)
		}
		return first
	}
	switch {
	case len(errs) > 0:
		return "error", more(errs[0]), detail
	case len(warns) > 0:
		return "warn", more(warns[0]), detail
	case len(syncing) > 0:
		return "syncing", more(syncing[0]), detail
	}
	n := 0
	for _, f := range rep.Folders {
		if !f.Paused {
			n++
		}
	}
	return "ok", fmt.Sprintf("In sync (%d folder%s)", n, plural(n)), nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// syncSummary is the device's status for the console (nil: never reported).
func (s *Server) syncSummary(d *store.Device) *SyncSummary {
	if d.Sync == "" || d.SyncAt == nil {
		return nil
	}
	var rep SyncReport
	if json.Unmarshal([]byte(d.Sync), &rep) != nil {
		return nil
	}
	sum := &SyncSummary{At: *d.SyncAt}
	sum.Level, sum.Text, sum.Detail = Summarize(&rep)
	if sum.Level != "none" && s.now().Sub(*d.SyncAt) > syncStale {
		sum.Level, sum.Text = "stale", "No report since "+d.SyncAt.UTC().Format("2006-01-02 15:04")+" UTC"
	}
	return sum
}

type agentSync struct {
	ID     string `json:"id"`
	TS     string `json:"ts"`
	Report string `json:"report"`
	Sig    string `json:"sig"`
}

func (s *Server) handleAgentSync(w http.ResponseWriter, r *http.Request) {
	var req agentSync
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	var rep SyncReport
	if len(req.Report) > maxSyncBytes || json.Unmarshal([]byte(req.Report), &rep) != nil {
		writeError(w, http.StatusBadRequest, "report must be JSON under 128 KB")
		return
	}
	dev, err := s.verifyAgent(r.Context(), req.ID, req.TS, req.Sig, SyncMessage(req.ID, req.TS, req.Report))
	if err != nil {
		s.cfg.Logger.Warn("agent sync rejected", "id", req.ID, "ip", s.clientIP(r), "err", err)
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	now := s.now()
	if err := s.store.SetDeviceSync(r.Context(), dev.ID, req.Report, now); err != nil {
		s.internalError(w, err)
		return
	}
	level, text, _ := Summarize(&rep)
	s.checkSync(dev, level, text, now)
	writeJSON(w, http.StatusOK, map[string]any{})
}

// checkSync alerts when a device has been out of sync for SyncAlert, and
// again when it is back in sync.
func (s *Server) checkSync(d *store.Device, level, text string, now time.Time) {
	if s.cfg.SyncAlert <= 0 {
		return
	}
	name := displayName(*d)
	s.mu.Lock()
	if s.syncSince == nil {
		s.syncSince = map[string]time.Time{}
		s.syncAlerted = map[string]bool{}
	}
	since, out := s.syncSince[d.ID]
	alerted := s.syncAlerted[d.ID]
	var msg, title string
	switch {
	case level == "ok" || level == "none":
		delete(s.syncSince, d.ID)
		delete(s.syncAlerted, d.ID)
		if alerted {
			title, msg = "RD: back in sync", fmt.Sprintf("%s: Syncthing is in sync again", name)
		}
	case !out:
		s.syncSince[d.ID] = now
	case !alerted && now.Sub(since) >= s.cfg.SyncAlert:
		s.syncAlerted[d.ID] = true
		title, msg = "RD: out of sync", fmt.Sprintf("%s: %s (for %s)", name, text, now.Sub(since).Round(time.Minute))
	}
	s.mu.Unlock()
	if msg != "" {
		s.notify(title, msg)
	}
}

func (s *Server) adminDeviceSync(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.GetDevice(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Device not found")
		return
	}
	if d.Sync == "" {
		writeJSON(w, http.StatusOK, map[string]any{"report": nil, "summary": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": json.RawMessage(d.Sync), "summary": s.syncSummary(d)})
}
