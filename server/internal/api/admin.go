package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zeonos/rd/server/internal/auth"
	"github.com/zeonos/rd/server/internal/store"
)

type deviceJSON struct {
	ID          string          `json:"id"`
	Alias       string          `json:"alias"`
	Hostname    string          `json:"hostname"`
	Username    string          `json:"username"`
	OS          string          `json:"os"`
	CPU         string          `json:"cpu"`
	Memory      string          `json:"memory"`
	Version     string          `json:"version"`
	Note        string          `json:"note"`
	GroupID     *int64          `json:"group_id"`
	Group       string          `json:"group"`
	Online      bool            `json:"online"`
	Conns       int             `json:"conns"`
	LastSeen    *time.Time      `json:"last_seen"`
	Unattended  bool            `json:"unattended"` // h1 reported: viewer can connect without a password
	CreatedAt   time.Time       `json:"created_at"`
	IP          string          `json:"ip"`
	Build       int             `json:"build"`
	InventoryAt *time.Time      `json:"inventory_at"`
	Disks       json.RawMessage `json:"disks,omitempty"` // [{name,label,size,free}] from the last inventory
}

func (s *Server) toDeviceJSON(d *store.Device) deviceJSON {
	j := deviceJSON{
		ID: d.ID, Alias: d.Alias, Hostname: d.Hostname, Username: d.Username, OS: d.OS, CPU: d.CPU,
		Memory: d.Memory, Version: d.Version, Note: d.Note, GroupID: d.GroupID, Group: groupName(d),
		Online: d.Online(s.now()), Conns: d.Conns, LastSeen: d.LastSeen,
		Unattended: d.PasswordH1 != "", CreatedAt: d.CreatedAt,
		IP: d.IP, Build: d.Build, InventoryAt: d.InventoryAt,
	}
	if d.Disks != "" {
		j.Disks = json.RawMessage(d.Disks)
	}
	return j
}

func (s *Server) adminListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.store.ListDevices(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]deviceJSON, 0, len(devices))
	for i := range devices {
		out = append(out, s.toDeviceJSON(&devices[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

type deviceUpdate struct {
	Alias *string `json:"alias"`
	Note  *string `json:"note"`
	// Group: path such as "Office/Rack 1" (created if missing); "" = unassigned.
	Group *string `json:"group"`
}

func (s *Server) adminUpdateDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req deviceUpdate
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	var gid **int64
	if req.Group != nil {
		var v *int64
		if strings.TrimSpace(*req.Group) != "" {
			g, err := s.store.EnsureGroupPath(r.Context(), *req.Group)
			if err != nil {
				s.internalError(w, err)
				return
			}
			v = &g.ID
		}
		gid = &v
	}
	if err := s.store.UpdateDevice(r.Context(), id, req.Alias, req.Note, gid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Device not found")
			return
		}
		s.internalError(w, err)
		return
	}
	d, err := s.store.GetDevice(r.Context(), id)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toDeviceJSON(d))
}

func (s *Server) adminDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteDevice(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Device not found")
			return
		}
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type groupJSON struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	ParentID *int64 `json:"parent_id"`
}

func (s *Server) adminListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListGroups(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]groupJSON, 0, len(groups))
	for _, g := range groups {
		out = append(out, groupJSON{ID: g.ID, Name: g.Name, Path: g.Path, ParentID: g.ParentID})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &req); err != nil || len(store.SplitPath(req.Path)) == 0 {
		writeError(w, http.StatusBadRequest, "path is required, e.g. \"Office/Rack 1\"")
		return
	}
	g, err := s.store.EnsureGroupPath(r.Context(), req.Path)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, groupJSON{ID: g.ID, Name: g.Name, Path: g.Path, ParentID: g.ParentID})
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

func (s *Server) adminRenameGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || !ok || strings.TrimSpace(req.Name) == "" || strings.Contains(req.Name, "/") {
		writeError(w, http.StatusBadRequest, "name is required and may not contain /")
		return
	}
	switch err := s.store.RenameGroup(r.Context(), id, strings.TrimSpace(req.Name)); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "Group not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "A group with that name already exists here")
	case err != nil:
		s.internalError(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) adminDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Bad id")
		return
	}
	switch err := s.store.DeleteGroup(r.Context(), id); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "Group not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "Group still has devices, subgroups or enrolment tokens")
	case err != nil:
		s.internalError(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

type TokenJSON struct {
	ID        int64      `json:"id"`
	Token     string     `json:"token,omitempty"` // only in the create response
	Group     string     `json:"group"`
	Note      string     `json:"note"`
	MaxUses   int        `json:"max_uses"`
	Uses      int        `json:"uses"`
	ExpiresAt *time.Time `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}

func toTokenJSON(t *store.EnrollToken) TokenJSON {
	return TokenJSON{ID: t.ID, Group: t.GroupPath, Note: t.Note, MaxUses: t.MaxUses, Uses: t.Uses,
		ExpiresAt: t.ExpiresAt, CreatedAt: t.CreatedAt}
}

func (s *Server) adminListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.ListEnrollTokens(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]TokenJSON, 0, len(tokens))
	for i := range tokens {
		out = append(out, toTokenJSON(&tokens[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateTokenRequest is shared with the CLI.
type CreateTokenRequest struct {
	Group      string `json:"group"` // "" = device picks with --device_group_name
	Note       string `json:"note"`
	MaxUses    int    `json:"max_uses"`
	ExpiresInH int    `json:"expires_in_hours"` // 0 = never
}

// CreateToken creates an enrolment token and returns it with its plain value.
func CreateToken(ctx context.Context, st store.Store, now time.Time, req CreateTokenRequest) (*TokenJSON, error) {
	var gid *int64
	if strings.TrimSpace(req.Group) != "" {
		g, err := st.EnsureGroupPath(ctx, req.Group)
		if err != nil {
			return nil, err
		}
		gid = &g.ID
	}
	plain, hash := auth.NewToken("rde_")
	t := &store.EnrollToken{TokenHash: hash, GroupID: gid, Note: req.Note, MaxUses: req.MaxUses}
	if req.ExpiresInH > 0 {
		exp := now.Add(time.Duration(req.ExpiresInH) * time.Hour)
		t.ExpiresAt = &exp
	}
	if err := st.CreateEnrollToken(ctx, t); err != nil {
		return nil, err
	}
	out := toTokenJSON(t)
	out.Token = plain
	if gid != nil {
		out.Group = store.JoinPath(store.SplitPath(req.Group))
	}
	return &out, nil
}

func (s *Server) adminCreateToken(w http.ResponseWriter, r *http.Request) {
	var req CreateTokenRequest
	if err := readJSON(r, &req); err != nil || req.MaxUses < 0 || req.ExpiresInH < 0 {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	out, err := CreateToken(r.Context(), s.store, s.now(), req)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminDeleteToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Bad id")
		return
	}
	if err := s.store.DeleteEnrollToken(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Token not found")
			return
		}
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
