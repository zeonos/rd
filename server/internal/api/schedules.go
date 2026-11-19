package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zeonos/rd/server/internal/store"
)

// Schedules run a saved script again and again: every N hours, or at a time
// of day on chosen weekdays in the admin's time zone. Each run is an
// ordinary job (linked to the schedule), so it shows up in Jobs and in the
// run history of the script and of each device.

type scheduleRequest struct {
	ScriptID      int64           `json:"script_id"`
	Kind          string          `json:"kind"`
	EveryHours    int             `json:"every_hours"`
	AtMinute      int             `json:"at_minute"`
	Weekdays      int             `json:"weekdays"`
	TZ            string          `json:"tz"`
	Target        store.JobTarget `json:"target"`
	Vars          store.Vars      `json:"vars"`
	DeadlineHours int             `json:"deadline_hours"`
	Enabled       *bool           `json:"enabled"`
}

// nextRun returns the first run time strictly after `after`.
func nextRun(sc *store.Schedule, after time.Time) time.Time {
	switch sc.Kind {
	case "hours":
		step := time.Duration(sc.EveryHours) * time.Hour
		next := sc.NextRun
		if next.IsZero() || next.After(after.Add(step)) {
			return after.Add(step)
		}
		for !next.After(after) {
			next = next.Add(step)
		}
		return next
	default: // days
		loc, err := time.LoadLocation(sc.TZ)
		if err != nil {
			loc = time.UTC
		}
		t := after.In(loc)
		for i := 0; i <= 8; i++ {
			d := t.AddDate(0, 0, i)
			cand := time.Date(d.Year(), d.Month(), d.Day(), sc.AtMinute/60, sc.AtMinute%60, 0, 0, loc)
			if cand.After(after) && sc.Weekdays&(1<<uint(cand.Weekday())) != 0 {
				return cand
			}
		}
		return after.Add(24 * time.Hour) // no weekday set; validation prevents this
	}
}

func (s *Server) scheduleFromRequest(ctx context.Context, req *scheduleRequest, sc *store.Schedule) error {
	if _, err := s.store.GetScript(ctx, req.ScriptID); err != nil {
		return userError{"Script not found"}
	}
	switch req.Kind {
	case "hours":
		if req.EveryHours < 1 || req.EveryHours > 24*7 {
			return userError{"Run every 1 to 168 hours"}
		}
	case "days":
		if req.AtMinute < 0 || req.AtMinute >= 24*60 {
			return userError{"Bad time of day"}
		}
		if req.Weekdays&0x7f == 0 {
			return userError{"Pick at least one weekday"}
		}
	default:
		return userError{"kind must be hours or days"}
	}
	if req.TZ == "" {
		req.TZ = "UTC"
	}
	if _, err := time.LoadLocation(req.TZ); err != nil {
		return userError{"Unknown time zone " + req.TZ}
	}
	if req.DeadlineHours == 0 {
		req.DeadlineHours = 1
	}
	if req.DeadlineHours < 1 || req.DeadlineHours > 24*7 {
		return userError{"Offline devices may catch up for 1 to 168 hours"}
	}
	req.Vars = cleanVars(req.Vars)
	if err := checkVars(req.Vars); err != nil {
		return userError{err.Error()}
	}
	jr := createJobRequest{Devices: req.Target.Devices, Groups: req.Target.Groups, All: req.Target.All}
	ids, text, err := s.resolveTargets(ctx, &jr)
	if err != nil {
		var nf notFoundTarget
		if errors.As(err, &nf) {
			return userError{err.Error()}
		}
		return err
	}
	if len(ids) == 0 && !req.Target.All && len(req.Target.Groups) == 0 {
		return userError{"No devices selected"}
	}
	changedTiming := sc.ID == 0 || sc.Kind != req.Kind || sc.EveryHours != req.EveryHours ||
		sc.AtMinute != req.AtMinute || sc.Weekdays != req.Weekdays || sc.TZ != req.TZ
	sc.ScriptID, sc.Kind, sc.EveryHours, sc.AtMinute, sc.Weekdays, sc.TZ = req.ScriptID, req.Kind, req.EveryHours, req.AtMinute, req.Weekdays&0x7f, req.TZ
	if sc.Kind == "hours" {
		sc.AtMinute, sc.Weekdays = 0, 0x7f
	} else {
		sc.EveryHours = 0
	}
	sc.Target, sc.TargetText, sc.Vars, sc.DeadlineHours = req.Target, text, req.Vars, req.DeadlineHours
	if req.Enabled != nil {
		sc.Enabled = *req.Enabled
	} else if sc.ID == 0 {
		sc.Enabled = true
	}
	if changedTiming {
		sc.NextRun = time.Time{}
		sc.NextRun = nextRun(sc, s.now())
	}
	return nil
}

func (s *Server) adminListSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSchedules(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	if list == nil {
		list = []store.Schedule{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) adminSaveSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request")
		return
	}
	sc := &store.Schedule{CreatedBy: userFrom(r).Name}
	if r.PathValue("id") != "" {
		id, ok := pathID(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "Bad id")
			return
		}
		old, err := s.store.GetSchedule(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "Schedule not found")
			return
		}
		sc = old
	}
	if err := s.scheduleFromRequest(r.Context(), &req, sc); err != nil {
		var ue userError
		if errors.As(err, &ue) {
			writeError(w, http.StatusBadRequest, ue.msg)
			return
		}
		s.internalError(w, err)
		return
	}
	if err := s.store.SaveSchedule(r.Context(), sc); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Script or schedule not found")
			return
		}
		s.internalError(w, err)
		return
	}
	s.cfg.Logger.Info("schedule saved", "schedule", sc.ID, "script", sc.ScriptName, "next", sc.NextRun, "by", userFrom(r).Name)
	writeJSON(w, http.StatusOK, sc)
}

func (s *Server) adminDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Bad id")
		return
	}
	if err := s.store.DeleteSchedule(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Schedule not found")
			return
		}
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminRunScheduleNow starts the schedule's job now; the timing is unchanged.
func (s *Server) adminRunScheduleNow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Bad id")
		return
	}
	sc, err := s.store.GetSchedule(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Schedule not found")
		return
	}
	j, msg, err := s.fireSchedule(r.Context(), sc, userFrom(r).Name)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if j == nil {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": j, "note": msg})
}

// fireSchedule creates the schedule's job. Devices missing a required
// variable are left out and named in the returned message. j is nil when
// nothing could be started.
func (s *Server) fireSchedule(ctx context.Context, sc *store.Schedule, by string) (*store.Job, string, error) {
	req := createJobRequest{ScriptID: sc.ScriptID, Devices: sc.Target.Devices, Groups: sc.Target.Groups, All: sc.Target.All,
		Vars: sc.Vars, DeadlineHours: sc.DeadlineHours}
	plan, err := s.planJob(ctx, &req, by)
	var ue userError
	if errors.As(err, &ue) {
		return nil, ue.msg, nil
	}
	if err != nil {
		return nil, "", err
	}
	note := ""
	if len(plan.missing) > 0 {
		note = "Skipped (variables without a value): " + plan.missingText()
		keep := plan.devices[:0]
		for _, id := range plan.devices {
			if len(plan.missing[id]) == 0 {
				keep = append(keep, id)
			}
		}
		plan.devices = keep
	}
	if len(plan.devices) == 0 {
		if note == "" {
			note = "No devices in the target"
		}
		return nil, note, nil
	}
	id := sc.ID
	plan.job.ScheduleID = &id
	plan.job.Name = sc.ScriptName
	if err := s.store.CreateJob(ctx, plan.job, plan.devices); err != nil {
		return nil, "", err
	}
	s.cfg.Logger.Info("schedule fired", "schedule", sc.ID, "job", plan.job.ID, "devices", len(plan.devices))
	return plan.job, note, nil
}

// runSchedules starts every due schedule once and moves it to its next time.
func (s *Server) runSchedules(ctx context.Context, now time.Time) {
	due, err := s.store.DueSchedules(ctx, now)
	if err != nil {
		s.cfg.Logger.Error("due schedules", "err", err)
		return
	}
	for i := range due {
		sc := &due[i]
		next := nextRun(sc, now)
		j, note, err := s.fireSchedule(ctx, sc, "schedule")
		if err != nil {
			s.cfg.Logger.Error("schedule", "schedule", sc.ID, "err", err)
			continue // retried next minute
		}
		var jobID *int64
		if j != nil {
			jobID = &j.ID
		}
		if note != "" {
			s.cfg.Logger.Warn("schedule", "schedule", sc.ID, "note", note)
			s.notify("RD: scheduled script", fmt.Sprintf("%s: %s", sc.ScriptName, strings.TrimSpace(note)))
		}
		if err := s.store.MarkScheduleRun(ctx, sc.ID, now, jobID, next); err != nil {
			s.cfg.Logger.Error("mark schedule", "schedule", sc.ID, "err", err)
		}
	}
}
