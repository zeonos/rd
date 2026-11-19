package api

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zeonos/rd/server/internal/store"
)

const (
	maxReleaseBytes   = 1 << 30
	maxInventoryBytes = 256 << 10
	// Update jobs: how long a device may take to pick one up, and how long to
	// wait before trying again after a failed or expired attempt.
	updateDeadline = 2 * time.Hour
	updateRetry    = 6 * time.Hour
)

// --- agent: inventory ---

type agentInventory struct {
	ID        string `json:"id"`
	TS        string `json:"ts"`
	Inventory string `json:"inventory"` // JSON document, exactly the signed bytes
	Sig       string `json:"sig"`
}

func InventoryMessage(id, ts, inventory string) string {
	sum := sha256.Sum256([]byte(inventory))
	return fmt.Sprintf("rd-inventory\n%s\n%s\n%s", id, hex.EncodeToString(sum[:]), ts)
}

// Inventory is the part of the agent's inventory the server reads itself.
type Inventory struct {
	Disks []struct {
		Name  string `json:"name"`
		Size  int64  `json:"size"`
		Free  int64  `json:"free"`
		Label string `json:"label"`
	} `json:"disks"`
}

func (s *Server) handleAgentInventory(w http.ResponseWriter, r *http.Request) {
	var req agentInventory
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	if len(req.Inventory) > maxInventoryBytes || !json.Valid([]byte(req.Inventory)) {
		writeError(w, http.StatusBadRequest, "inventory must be JSON under 256 KB")
		return
	}
	dev, err := s.verifyAgent(r.Context(), req.ID, req.TS, req.Sig, InventoryMessage(req.ID, req.TS, req.Inventory))
	if err != nil {
		s.cfg.Logger.Warn("agent inventory rejected", "id", req.ID, "ip", s.clientIP(r), "err", err)
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	now := s.now()
	if err := s.store.SetDeviceInventory(r.Context(), dev.ID, req.Inventory, diskSummary(req.Inventory), now); err != nil {
		s.internalError(w, err)
		return
	}
	s.checkDisks(dev, req.Inventory, now)
	writeJSON(w, http.StatusOK, map[string]any{})
}

// diskSummary is the inventory's disk list as compact JSON ("" if none).
func diskSummary(inventory string) string {
	var inv struct {
		Disks json.RawMessage `json:"disks"`
	}
	if json.Unmarshal([]byte(inventory), &inv) != nil || len(inv.Disks) == 0 || inv.Disks[0] != '[' {
		return ""
	}
	return string(inv.Disks)
}

// checkDisks alerts once a day per disk that has less free space than
// DiskAlertPercent.
func (s *Server) checkDisks(d *store.Device, inventory string, now time.Time) {
	if s.cfg.DiskAlertPercent <= 0 {
		return
	}
	var inv Inventory
	if json.Unmarshal([]byte(inventory), &inv) != nil {
		return
	}
	for _, disk := range inv.Disks {
		if disk.Size <= 0 {
			continue
		}
		pct := float64(disk.Free) * 100 / float64(disk.Size)
		if pct >= float64(s.cfg.DiskAlertPercent) {
			continue
		}
		key := d.ID + "|" + disk.Name
		s.mu.Lock()
		if s.diskAlerts == nil {
			s.diskAlerts = map[string]time.Time{}
		}
		last, seen := s.diskAlerts[key]
		if seen && now.Sub(last) < 24*time.Hour {
			s.mu.Unlock()
			continue
		}
		s.diskAlerts[key] = now
		s.mu.Unlock()
		s.notify("RD: low disk space", fmt.Sprintf("%s (%s): %s has %.1f GB free of %.1f GB (%.0f%%)",
			deviceName(d), groupName(d), disk.Name, float64(disk.Free)/1e9, float64(disk.Size)/1e9, pct))
	}
}

func deviceName(d *store.Device) string {
	if d.Alias != "" {
		return d.Alias
	}
	if d.Hostname != "" {
		return d.Hostname
	}
	return d.ID
}

func (s *Server) adminDeviceInventory(w http.ResponseWriter, r *http.Request) {
	inv, err := s.store.GetDeviceInventory(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Device not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if inv == "" {
		inv = "null"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(inv))
}

// --- releases ---

func (s *Server) releaseDir() string { return filepath.Join(s.cfg.DataDir, "releases") }

func (s *Server) releasePath(build int) string {
	return filepath.Join(s.releaseDir(), fmt.Sprintf("rd-windows-x64-%d.zip", build))
}

// readReleaseInfo finds rd-build.txt (and rd-version.txt) in the CI zip and
// checks that it carries RD.exe and install.ps1.
func readReleaseInfo(path string) (build int, version string, err error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return 0, "", errors.New("not a zip file")
	}
	defer zr.Close()
	var hasExe, hasInstaller bool
	read := func(f *zip.File) string {
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		defer rc.Close()
		b, _ := io.ReadAll(io.LimitReader(rc, 256))
		return strings.TrimSpace(string(b))
	}
	for _, f := range zr.File {
		base := strings.ToLower(filepath.Base(f.Name))
		switch base {
		case "rd-build.txt":
			build, _ = strconv.Atoi(read(f))
		case "rd-version.txt":
			version = read(f)
		case "rd.exe":
			hasExe = true
		case "install.ps1":
			hasInstaller = true
		}
	}
	if !hasExe || !hasInstaller {
		return 0, "", errors.New("the zip has no RD.exe or install.ps1: upload rd-windows-x64.zip from the Windows client build")
	}
	if build <= 0 {
		return 0, "", errors.New("the zip has no rd-build.txt: it comes from a build before auto-update support")
	}
	return build, version, nil
}

func (s *Server) adminUploadRelease(w http.ResponseWriter, r *http.Request) {
	if err := os.MkdirAll(s.releaseDir(), 0o750); err != nil {
		s.internalError(w, err)
		return
	}
	tmp, err := os.CreateTemp(s.releaseDir(), "upload-*.zip")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), r.Body)
	tmp.Close()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Upload failed or larger than 1 GB")
		return
	}
	build, version, err := readReleaseInfo(tmp.Name())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rel := &store.Release{Build: build, Version: version, SHA256: hex.EncodeToString(h.Sum(nil)), Size: size}
	if err := s.store.AddRelease(r.Context(), rel); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, fmt.Sprintf("Build %d is already uploaded", build))
			return
		}
		s.internalError(w, err)
		return
	}
	if err := os.Rename(tmp.Name(), s.releasePath(build)); err != nil {
		s.store.DeleteRelease(r.Context(), build)
		s.internalError(w, err)
		return
	}
	s.cfg.Logger.Info("release uploaded", "build", build, "version", version, "by", userFrom(r).Name)
	writeJSON(w, http.StatusOK, rel)
}

type releaseJSON struct {
	store.Release
	Devices int `json:"devices"` // devices currently on this build
}

func (s *Server) adminListReleases(w http.ResponseWriter, r *http.Request) {
	rels, err := s.store.ListReleases(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	devs, err := s.store.ListDevices(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	count := map[int]int{}
	for _, d := range devs {
		count[d.Build]++
	}
	out := make([]releaseJSON, 0, len(rels))
	for _, rel := range rels {
		out = append(out, releaseJSON{Release: rel, Devices: count[rel.Build]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": out, "unknown_build_devices": count[0]})
}

func (s *Server) adminSetCurrentRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Build int `json:"build"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	if err := s.store.SetCurrentRelease(r.Context(), req.Build); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Release not found")
			return
		}
		s.internalError(w, err)
		return
	}
	s.cfg.Logger.Info("current release set", "build", req.Build, "by", userFrom(r).Name)
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) adminDeleteRelease(w http.ResponseWriter, r *http.Request) {
	build, err := strconv.Atoi(r.PathValue("build"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Bad build")
		return
	}
	rels, err := s.store.ListReleases(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	for _, rel := range rels {
		if rel.Build == build && rel.Current {
			writeError(w, http.StatusConflict, "Stop rolling out this build before deleting it")
			return
		}
	}
	if err := s.store.DeleteRelease(r.Context(), build); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Release not found")
			return
		}
		s.internalError(w, err)
		return
	}
	os.Remove(s.releasePath(build))
	writeJSON(w, http.StatusOK, map[string]any{})
}

// handleReleaseDownload serves release zips to agents. They are not secret:
// the update job carries their SHA-256, signed by the job key.
func (s *Server) handleReleaseDownload(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(r.PathValue("file"), ".zip")
	build, err := strconv.Atoi(name)
	if err != nil || build <= 0 {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(s.releasePath(build))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	http.ServeContent(w, r, filepath.Base(f.Name()), st.ModTime(), f)
}

// UpdateBody is the body of an "update" job: what to download and its hash.
type UpdateBody struct {
	Build  int    `json:"build"`
	Path   string `json:"path"` // relative to the API server URL
	SHA256 string `json:"sha256"`
}

func updateJobName(build int) string { return fmt.Sprintf("Update to build %d", build) }

// autoUpdate gives every online device on an older RD build an update job
// for the current release, at most once per updateRetry.
func (s *Server) autoUpdate(ctx context.Context, now time.Time) {
	rels, err := s.store.ListReleases(ctx)
	if err != nil {
		s.cfg.Logger.Error("auto-update", "err", err)
		return
	}
	var cur *store.Release
	for i := range rels {
		if rels[i].Current {
			cur = &rels[i]
		}
	}
	if cur == nil {
		return
	}
	devs, err := s.store.ListDevices(ctx)
	if err != nil {
		s.cfg.Logger.Error("auto-update", "err", err)
		return
	}
	body, _ := json.Marshal(UpdateBody{Build: cur.Build, Path: fmt.Sprintf("/api/releases/%d.zip", cur.Build), SHA256: cur.SHA256})
	name := updateJobName(cur.Build)
	for i := range devs {
		d := &devs[i]
		// Build 0: a client from before auto-update, which cannot run update jobs.
		if d.Build == 0 || d.Build >= cur.Build || !d.Online(now) {
			continue
		}
		recent, err := s.store.HasRecentJob(ctx, d.ID, name, now.Add(-updateRetry))
		if err != nil || recent {
			continue
		}
		j := &store.Job{Kind: "update", Name: name, Body: string(body), RunAs: "system", TimeoutSec: 900,
			Target: deviceName(d), CreatedBy: "auto-update", NotBefore: now, Deadline: now.Add(updateDeadline)}
		if err := s.store.CreateJob(ctx, j, []string{d.ID}); err != nil {
			s.cfg.Logger.Error("auto-update job", "device", d.ID, "err", err)
			continue
		}
		s.cfg.Logger.Info("auto-update queued", "device", d.ID, "from", d.Build, "to", cur.Build, "job", j.ID)
	}
}
