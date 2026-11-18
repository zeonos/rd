package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/zeonos/rd/server/internal/policy"
	"github.com/zeonos/rd/server/internal/store"
)

// layersFor returns the policy layers that apply to a device in groupID:
// account first, then each group from the top of the path down.
func (s *Server) layersFor(ctx context.Context, groupID *int64) ([]policy.Layer, error) {
	pols, err := s.store.ListPolicies(ctx)
	if err != nil {
		return nil, err
	}
	layers := []policy.Layer{{Name: "Account", Values: pols[store.AccountScope]}}
	if groupID == nil {
		return layers, nil
	}
	groups, err := s.store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]store.Group, len(groups))
	for _, g := range groups {
		byID[g.ID] = g
	}
	var chain []store.Group
	seen := map[int64]bool{}
	for id := groupID; id != nil && !seen[*id]; {
		g, ok := byID[*id]
		if !ok {
			break
		}
		seen[g.ID] = true
		chain = append([]store.Group{g}, chain...)
		id = g.ParentID
	}
	for _, g := range chain {
		if v := pols[g.ID]; len(v) > 0 {
			layers = append(layers, policy.Layer{Name: g.Path, Values: v})
		}
	}
	return layers, nil
}

func (s *Server) effectivePolicy(ctx context.Context, groupID *int64) (map[string]policy.Resolved, error) {
	layers, err := s.layersFor(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return policy.Resolve(layers), nil
}

// strategyFor builds the heartbeat fields that push settings to a device,
// or nil when the device already has the current version.
func (s *Server) strategyFor(ctx context.Context, d *store.Device, clientVersion int64) map[string]any {
	r, err := s.effectivePolicy(ctx, d.GroupID)
	if err != nil {
		s.cfg.Logger.Error("policy", "err", err)
		return nil
	}
	opts := policy.ConfigOptions(r)
	v := policy.Version(opts)
	if v == clientVersion {
		return nil
	}
	return map[string]any{
		"strategy":    map[string]any{"config_options": opts},
		"modified_at": v,
	}
}

// --- admin endpoints ---

func (s *Server) adminPolicyCatalogue(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, policy.Catalogue)
}

type scopePolicy struct {
	Scope  string            `json:"scope"` // "account" or the group id
	Name   string            `json:"name"`  // "Account" or the group path
	Values map[string]string `json:"values"`
}

func (s *Server) adminListPolicies(w http.ResponseWriter, r *http.Request) {
	pols, err := s.store.ListPolicies(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	groups, err := s.store.ListGroups(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	acc := pols[store.AccountScope]
	if acc == nil {
		acc = map[string]string{}
	}
	out := []scopePolicy{{Scope: "account", Name: "Account", Values: acc}}
	for _, g := range groups {
		v := pols[g.ID]
		if v == nil {
			v = map[string]string{}
		}
		out = append(out, scopePolicy{Scope: strconv.FormatInt(g.ID, 10), Name: g.Path, Values: v})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminSetPolicy(w http.ResponseWriter, r *http.Request) {
	scope := store.AccountScope
	if sc := r.PathValue("scope"); sc != "account" {
		id, err := strconv.ParseInt(sc, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "scope must be \"account\" or a group id")
			return
		}
		scope = id
	}
	var req struct {
		Values map[string]string `json:"values"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	vals, err := policy.Validate(req.Values)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SetPolicy(r.Context(), scope, vals); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Group not found")
			return
		}
		s.internalError(w, err)
		return
	}
	s.cfg.Logger.Info("policy changed", "scope", scope, "by", userFrom(r).Name)
	writeJSON(w, http.StatusOK, map[string]any{"values": vals})
}

func (s *Server) adminDevicePolicy(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.GetDevice(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Device not found")
			return
		}
		s.internalError(w, err)
		return
	}
	res, err := s.effectivePolicy(r.Context(), d.GroupID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
