package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zeonos/rd/server/internal/store"
)

// Script variables
//
// A job's script gets variables as plain assignments put in front of the
// body when the agent fetches it: `$IP = '10.0.0.5'` for PowerShell,
// `@set "IP=10.0.0.5"` for the command prompt. Values come, in order of
// precedence, from the value typed when starting the job, the device, its
// location (the nearest one up the tree wins), and the script's default.
// Built-ins (RD_*) describe the device. Values are resolved when the job is
// created and stored with each run, so the run log shows what was used.

var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// PowerShell names that cannot be assigned (constants and read-only
// automatic variables).
var reservedVars = map[string]bool{
	"true": true, "false": true, "null": true, "host": true, "pid": true, "home": true, "pshome": true,
	"shellid": true, "executioncontext": true, "psversiontable": true, "psculture": true, "psuiculture": true,
	"args": true, "input": true, "this": true, "_": true, "error": true, "myinvocation": true, "psitem": true,
	"psscriptroot": true, "pscommandpath": true, "lastexitcode": true, "matches": true, "foreach": true,
	"switch": true, "sender": true, "event": true, "eventargs": true, "eventsubscriber": true, "stacktrace": true,
	"psboundparameters": true, "pscmdlet": true, "profile": true, "nestedpromptlevel": true, "?": true, "^": true, "$": true,
}

const (
	maxVars       = 100
	maxVarValue   = 4096
	builtinPrefix = "RD_"
)

func checkVarName(n string) error {
	if !varName.MatchString(n) {
		return fmt.Errorf("%q is not a valid variable name: use letters, digits and _, starting with a letter", n)
	}
	if reservedVars[strings.ToLower(n)] {
		return fmt.Errorf("%q is a reserved PowerShell variable", n)
	}
	if strings.HasPrefix(strings.ToUpper(n), builtinPrefix) {
		return fmt.Errorf("%q: names starting with RD_ are built in", n)
	}
	return nil
}

func checkVars(v store.Vars) error {
	if len(v) > maxVars {
		return fmt.Errorf("at most %d variables", maxVars)
	}
	for k, x := range v {
		if err := checkVarName(k); err != nil {
			return err
		}
		if len(x) > maxVarValue {
			return fmt.Errorf("value of %s is longer than %d characters", k, maxVarValue)
		}
		if strings.ContainsAny(x, "\r\n\x00") {
			return fmt.Errorf("value of %s must be a single line", k)
		}
	}
	return nil
}

// cleanVars drops empty values and trims names.
func cleanVars(v store.Vars) store.Vars {
	out := store.Vars{}
	for k, x := range v {
		k = strings.TrimSpace(k)
		if k != "" && x != "" {
			out[k] = x
		}
	}
	return out
}

// Prelude renders variables as script lines for the shell.
func Prelude(shell string, v store.Vars) string {
	if len(v) == 0 {
		return ""
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		x := strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(v[k])
		if shell == "cmd" {
			fmt.Fprintf(&b, "@set \"%s=%s\"\r\n", k, strings.ReplaceAll(x, "%", "%%"))
		} else {
			fmt.Fprintf(&b, "$%s = '%s'\r\n", k, strings.ReplaceAll(x, "'", "''"))
		}
	}
	return b.String()
}

// varContext caches what variable resolution needs for one job.
type varContext struct {
	devices map[string]store.Device
	groups  map[int64]store.Group
	devVars map[string]store.Vars
	grpVars map[int64]store.Vars
}

func (s *Server) loadVarContext(ctx context.Context) (*varContext, error) {
	devs, err := s.store.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	grps, err := s.store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	dv, gv, err := s.store.AllVars(ctx)
	if err != nil {
		return nil, err
	}
	vc := &varContext{devices: map[string]store.Device{}, groups: map[int64]store.Group{}, devVars: dv, grpVars: gv}
	for _, d := range devs {
		vc.devices[d.ID] = d
	}
	for _, g := range grps {
		vc.groups[g.ID] = g
	}
	return vc, nil
}

func displayName(d store.Device) string {
	switch {
	case d.Alias != "":
		return d.Alias
	case d.Hostname != "":
		return d.Hostname
	}
	return d.ID
}

// resolve returns the variables for one device and the names of required
// parameters without a value.
func (vc *varContext) resolve(id string, params []store.ScriptParam, override store.Vars) (store.Vars, []string) {
	out := store.Vars{}
	d := vc.devices[id]
	// script defaults first, so anything more specific wins
	for _, p := range params {
		if p.Default != "" {
			out[p.Name] = p.Default
		}
	}
	// locations from the root down to the device's own
	var chain []int64
	for gid := d.GroupID; gid != nil; {
		g, ok := vc.groups[*gid]
		if !ok || len(chain) > 64 {
			break
		}
		chain = append(chain, g.ID)
		gid = g.ParentID
	}
	for i := len(chain) - 1; i >= 0; i-- {
		for k, v := range vc.grpVars[chain[i]] {
			out[k] = v
		}
	}
	for k, v := range vc.devVars[id] {
		out[k] = v
	}
	for k, v := range override {
		if v != "" {
			out[k] = v
		}
	}
	location := ""
	if d.GroupID != nil {
		location = vc.groups[*d.GroupID].Path
	}
	out["RD_DEVICE_ID"] = d.ID
	out["RD_DEVICE_NAME"] = displayName(d)
	out["RD_HOSTNAME"] = d.Hostname
	out["RD_LOCATION"] = location
	out["RD_PUBLIC_IP"] = d.IP
	var missing []string
	for _, p := range params {
		if out[p.Name] == "" {
			missing = append(missing, p.Name)
		}
	}
	return out, missing
}

func validateParams(sc *store.Script) error {
	if sc.Params == nil {
		sc.Params = []store.ScriptParam{}
	}
	if len(sc.Params) > 50 {
		return errors.New("at most 50 variables per script")
	}
	seen := map[string]bool{}
	for i := range sc.Params {
		p := &sc.Params[i]
		p.Name = strings.TrimSpace(p.Name)
		if err := checkVarName(p.Name); err != nil {
			return err
		}
		if seen[strings.ToLower(p.Name)] {
			return fmt.Errorf("variable %s is listed twice", p.Name)
		}
		seen[strings.ToLower(p.Name)] = true
		if err := checkVars(store.Vars{p.Name: p.Default}); err != nil {
			return err
		}
		if len(p.Description) > 500 {
			return fmt.Errorf("description of %s is too long", p.Name)
		}
	}
	return nil
}

// --- variables API ---

type varsBody struct {
	Devices map[string]store.Vars `json:"devices"`
	Groups  map[string]store.Vars `json:"groups"` // keyed by location ID
}

func (s *Server) adminGetVars(w http.ResponseWriter, r *http.Request) {
	dv, gv, err := s.store.AllVars(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	scripts, err := s.store.ListScripts(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	names := map[string]bool{}
	groups := map[string]store.Vars{}
	for id, v := range gv {
		groups[strconv.FormatInt(id, 10)] = v
		for k := range v {
			names[k] = true
		}
	}
	for _, v := range dv {
		for k := range v {
			names[k] = true
		}
	}
	usedBy := map[string][]string{}
	for _, sc := range scripts {
		for _, p := range sc.Params {
			names[p.Name] = true
			usedBy[p.Name] = append(usedBy[p.Name], sc.Name)
		}
	}
	list := make([]string, 0, len(names))
	for k := range names {
		list = append(list, k)
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i]) < strings.ToLower(list[j]) })
	writeJSON(w, http.StatusOK, map[string]any{"devices": dv, "groups": groups, "names": list, "used_by": usedBy,
		"builtins": []string{"RD_DEVICE_ID", "RD_DEVICE_NAME", "RD_HOSTNAME", "RD_LOCATION", "RD_PUBLIC_IP"}})
}

// adminSetVars replaces the variables of every device and location named in
// the body; others are left alone.
func (s *Server) adminSetVars(w http.ResponseWriter, r *http.Request) {
	var body varsBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	gids := map[int64]store.Vars{}
	for k, v := range body.Groups {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Bad location id "+k)
			return
		}
		gids[id] = cleanVars(v)
	}
	for id, v := range body.Devices {
		body.Devices[id] = cleanVars(v)
		if err := checkVars(body.Devices[id]); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	for _, v := range gids {
		if err := checkVars(v); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	for id, v := range body.Devices {
		if err := s.store.SetDeviceVars(r.Context(), id, v); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusBadRequest, "Device "+id+" not found")
				return
			}
			s.internalError(w, err)
			return
		}
	}
	for id, v := range gids {
		if err := s.store.SetGroupVars(r.Context(), id, v); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("Location %d not found", id))
				return
			}
			s.internalError(w, err)
			return
		}
	}
	s.cfg.Logger.Info("variables saved", "devices", len(body.Devices), "locations", len(gids), "by", userFrom(r).Name)
	w.WriteHeader(http.StatusNoContent)
}

// --- run history ---

func (s *Server) adminDeviceRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.ListDeviceRuns(r.Context(), r.PathValue("id"), 50)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if runs == nil {
		runs = []store.DeviceRun{}
	}
	writeJSON(w, http.StatusOK, runs)
}

// --- import / export ---

const exportFormat = "rd-scripts"

type exportedScript struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Shell       string              `json:"shell"`
	RunAs       string              `json:"run_as"`
	TimeoutSec  int                 `json:"timeout_sec"`
	Params      []store.ScriptParam `json:"params"`
	Body        string              `json:"body"`
}

type scriptBundle struct {
	Format     string           `json:"format"`
	Version    int              `json:"version"`
	ExportedAt time.Time        `json:"exported_at"`
	Scripts    []exportedScript `json:"scripts"`
}

func (s *Server) adminExportScripts(w http.ResponseWriter, r *http.Request) {
	all, err := s.store.ListScripts(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	want := map[int64]bool{}
	for _, x := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
			want[id] = true
		}
	}
	b := scriptBundle{Format: exportFormat, Version: 1, ExportedAt: s.now().UTC(), Scripts: []exportedScript{}}
	for _, sc := range all {
		if len(want) > 0 && !want[sc.ID] {
			continue
		}
		b.Scripts = append(b.Scripts, exportedScript{Name: sc.Name, Description: sc.Description, Shell: sc.Shell,
			RunAs: sc.RunAs, TimeoutSec: sc.TimeoutSec, Params: sc.Params, Body: sc.Body})
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="rd-scripts-%s.json"`, s.now().Format("2006-01-02")))
	writeJSON(w, http.StatusOK, b)
}

type importRequest struct {
	scriptBundle
	// Replace overwrites scripts with the same name; otherwise they are skipped.
	Replace bool `json:"replace"`
}

func (s *Server) adminImportScripts(w http.ResponseWriter, r *http.Request) {
	var req importRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "This is not an RD script export (bad JSON)")
		return
	}
	if req.Format != exportFormat || req.Version != 1 {
		writeError(w, http.StatusBadRequest, "This is not an RD script export")
		return
	}
	if len(req.Scripts) > 500 {
		writeError(w, http.StatusBadRequest, "At most 500 scripts per import")
		return
	}
	existing, err := s.store.ListScripts(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	byName := map[string]int64{}
	for _, sc := range existing {
		byName[strings.ToLower(sc.Name)] = sc.ID
	}
	var created, updated, skipped []string
	errs := []string{}
	for _, x := range req.Scripts {
		sc := store.Script{Name: x.Name, Description: x.Description, Shell: x.Shell, RunAs: x.RunAs,
			TimeoutSec: x.TimeoutSec, Params: x.Params, Body: x.Body}
		if err := validateScript(&sc); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", x.Name, err))
			continue
		}
		if id, ok := byName[strings.ToLower(sc.Name)]; ok {
			if !req.Replace {
				skipped = append(skipped, sc.Name)
				continue
			}
			sc.ID = id
		}
		if err := s.store.SaveScript(r.Context(), &sc); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", x.Name, err))
			continue
		}
		if _, ok := byName[strings.ToLower(sc.Name)]; ok {
			updated = append(updated, sc.Name)
		} else {
			created = append(created, sc.Name)
			byName[strings.ToLower(sc.Name)] = sc.ID
		}
	}
	s.cfg.Logger.Info("scripts imported", "created", len(created), "updated", len(updated), "skipped", len(skipped), "by", userFrom(r).Name)
	writeJSON(w, http.StatusOK, map[string]any{"created": len(created), "updated": len(updated), "skipped": skipped, "errors": errs})
}
