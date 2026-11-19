package store

import (
	"context"
	"sort"
	"time"
)

type memLibrary struct {
	devVars   map[string]Vars
	groupVars map[int64]Vars
	schedules map[int64]*Schedule
}

func (m *Memory) l() *memLibrary {
	if m.lib == nil {
		m.lib = &memLibrary{devVars: map[string]Vars{}, groupVars: map[int64]Vars{}, schedules: map[int64]*Schedule{}}
	}
	return m.lib
}

func (m *Memory) AllVars(ctx context.Context) (map[string]Vars, map[int64]Vars, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dv := map[string]Vars{}
	for id, v := range m.l().devVars {
		if _, ok := m.devices[id]; ok && len(v) > 0 {
			dv[id] = copyVars(v)
		}
	}
	gv := map[int64]Vars{}
	for id, v := range m.l().groupVars {
		if _, ok := m.groups[id]; ok && len(v) > 0 {
			gv[id] = copyVars(v)
		}
	}
	return dv, gv, nil
}

func (m *Memory) SetDeviceVars(ctx context.Context, id string, v Vars) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.devices[id]; !ok {
		return ErrNotFound
	}
	m.l().devVars[id] = copyVars(v)
	return nil
}

func (m *Memory) SetGroupVars(ctx context.Context, id int64, v Vars) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.groups[id]; !ok {
		return ErrNotFound
	}
	m.l().groupVars[id] = copyVars(v)
	return nil
}

func (m *Memory) ListDeviceRuns(ctx context.Context, deviceID string, limit int) ([]DeviceRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DeviceRun
	for id, runs := range m.j().runs {
		r, ok := runs[deviceID]
		if !ok {
			continue
		}
		j := m.j().jobs[id]
		c := *r
		c.Vars = copyVars(r.Vars)
		out = append(out, DeviceRun{JobRun: c, JobName: j.Name, Kind: j.Kind, ScriptID: j.ScriptID, CreatedAt: j.CreatedAt, CreatedBy: j.CreatedBy})
	}
	sort.Slice(out, func(i, k int) bool { return out[i].JobID > out[k].JobID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) copySchedule(s *Schedule) Schedule {
	c := *s
	c.Vars = copyVars(s.Vars)
	c.Target.Devices = append([]string(nil), s.Target.Devices...)
	c.Target.Groups = append([]int64(nil), s.Target.Groups...)
	if sc, ok := m.j().scripts[s.ScriptID]; ok {
		c.ScriptName = sc.Name
	}
	return c
}

func (m *Memory) ListSchedules(ctx context.Context) ([]Schedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Schedule, 0, len(m.l().schedules))
	for _, s := range m.l().schedules {
		if _, ok := m.j().scripts[s.ScriptID]; !ok {
			continue // script deleted: schedule goes with it
		}
		out = append(out, m.copySchedule(s))
	}
	sort.Slice(out, func(i, k int) bool { return out[i].ID < out[k].ID })
	return out, nil
}

func (m *Memory) GetSchedule(ctx context.Context, id int64) (*Schedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.l().schedules[id]
	if !ok {
		return nil, ErrNotFound
	}
	if _, ok := m.j().scripts[s.ScriptID]; !ok {
		return nil, ErrNotFound
	}
	c := m.copySchedule(s)
	return &c, nil
}

func (m *Memory) SaveSchedule(ctx context.Context, s *Schedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.j().scripts[s.ScriptID]; !ok {
		return ErrNotFound
	}
	if s.ID == 0 {
		s.ID = m.id()
		s.CreatedAt = m.nowSource()
	} else if old, ok := m.l().schedules[s.ID]; ok {
		s.CreatedAt, s.CreatedBy, s.LastRun, s.LastJobID = old.CreatedAt, old.CreatedBy, old.LastRun, old.LastJobID
	} else {
		return ErrNotFound
	}
	c := m.copySchedule(s)
	s.ScriptName = c.ScriptName
	m.l().schedules[s.ID] = &c
	return nil
}

func (m *Memory) DeleteSchedule(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.l().schedules[id]; !ok {
		return ErrNotFound
	}
	delete(m.l().schedules, id)
	return nil
}

func (m *Memory) DueSchedules(ctx context.Context, now time.Time) ([]Schedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Schedule
	for _, s := range m.l().schedules {
		if _, ok := m.j().scripts[s.ScriptID]; ok && s.Enabled && !now.Before(s.NextRun) {
			out = append(out, m.copySchedule(s))
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].ID < out[k].ID })
	return out, nil
}

func (m *Memory) MarkScheduleRun(ctx context.Context, id int64, ranAt time.Time, jobID *int64, next time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.l().schedules[id]
	if !ok {
		return ErrNotFound
	}
	t := ranAt
	s.LastRun = &t
	if jobID != nil {
		j := *jobID
		s.LastJobID = &j
	}
	s.NextRun = next
	return nil
}
