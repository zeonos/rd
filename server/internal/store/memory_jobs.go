package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

type memJobs struct {
	serverKeys map[string]string
	scripts    map[int64]*Script
	jobs       map[int64]*Job
	runs       map[int64]map[string]*JobRun // job -> device -> run
}

func (m *Memory) j() *memJobs {
	if m.jobState == nil {
		m.jobState = &memJobs{serverKeys: map[string]string{}, scripts: map[int64]*Script{},
			jobs: map[int64]*Job{}, runs: map[int64]map[string]*JobRun{}}
	}
	return m.jobState
}

func (m *Memory) SetDevicePublicKey(ctx context.Context, id, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return ErrNotFound
	}
	d.PublicKey = key
	return nil
}

func (m *Memory) GetServerKey(ctx context.Context, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.j().serverKeys[name]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *Memory) PutServerKeyIfAbsent(ctx context.Context, name, value string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.j().serverKeys[name]; ok {
		return v, nil
	}
	m.j().serverKeys[name] = value
	return value, nil
}

func (m *Memory) ListScripts(ctx context.Context) ([]Script, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Script, 0, len(m.j().scripts))
	for _, s := range m.j().scripts {
		c := *s
		c.Params = append([]ScriptParam{}, s.Params...)
		out = append(out, c)
	}
	sort.Slice(out, func(i, k int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[k].Name) })
	return out, nil
}

func (m *Memory) GetScript(ctx context.Context, id int64) (*Script, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.j().scripts[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := *s
	c.Params = append([]ScriptParam(nil), s.Params...)
	return &c, nil
}

func (m *Memory) SaveScript(ctx context.Context, s *Script) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.j().scripts {
		if o.ID != s.ID && strings.EqualFold(o.Name, s.Name) {
			return ErrConflict
		}
	}
	now := m.nowSource()
	if s.ID == 0 {
		s.ID = m.id()
		s.CreatedAt = now
	} else if old, ok := m.j().scripts[s.ID]; ok {
		s.CreatedAt = old.CreatedAt
	} else {
		return ErrNotFound
	}
	s.UpdatedAt = now
	if s.Params == nil {
		s.Params = []ScriptParam{}
	}
	c := *s
	c.Params = append([]ScriptParam{}, s.Params...)
	m.j().scripts[s.ID] = &c
	return nil
}

func (m *Memory) DeleteScript(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.j().scripts[id]; !ok {
		return ErrNotFound
	}
	delete(m.j().scripts, id)
	for sid, sc := range m.l().schedules {
		if sc.ScriptID == id {
			delete(m.l().schedules, sid)
		}
	}
	return nil
}

func (m *Memory) CreateJob(ctx context.Context, j *Job, deviceIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.ID = m.id()
	j.CreatedAt = m.nowSource()
	c := *j
	c.RunVars, c.Vars, c.Counts = nil, nil, nil
	m.j().jobs[j.ID] = &c
	runs := map[string]*JobRun{}
	for _, d := range deviceIDs {
		runs[d] = &JobRun{JobID: j.ID, DeviceID: d, Status: RunQueued, Vars: copyVars(j.RunVars[d])}
	}
	m.j().runs[j.ID] = runs
	return nil
}

func (m *Memory) ListJobs(ctx context.Context, f JobFilter) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	limit := f.Limit
	out := make([]Job, 0, len(m.j().jobs))
	for _, j := range m.j().jobs {
		if f.ScriptID != 0 && (j.ScriptID == nil || *j.ScriptID != f.ScriptID) {
			continue
		}
		if f.ScheduleID != 0 && (j.ScheduleID == nil || *j.ScheduleID != f.ScheduleID) {
			continue
		}
		c := *j
		c.Counts = map[string]int{}
		for _, r := range m.j().runs[j.ID] {
			c.Counts[r.Status]++
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].ID > out[k].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) GetJob(ctx context.Context, id int64) (*Job, []JobRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.j().jobs[id]
	if !ok {
		return nil, nil, ErrNotFound
	}
	c := *j
	runs := make([]JobRun, 0, len(m.j().runs[id]))
	for _, r := range m.j().runs[id] {
		c := *r
		c.Vars = copyVars(r.Vars)
		runs = append(runs, c)
	}
	sort.Slice(runs, func(i, k int) bool { return runs[i].DeviceID < runs[k].DeviceID })
	return &c, runs, nil
}

func (m *Memory) CancelJob(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	runs, ok := m.j().runs[id]
	if !ok {
		return ErrNotFound
	}
	now := m.nowSource()
	for _, r := range runs {
		if r.Status == RunQueued {
			r.Status = RunCancelled
			r.FinishedAt = &now
		}
	}
	return nil
}

func (m *Memory) ClaimRuns(ctx context.Context, deviceID string, now time.Time) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Job
	for id, runs := range m.j().runs {
		r, ok := runs[deviceID]
		if !ok || r.Status != RunQueued {
			continue
		}
		j := m.j().jobs[id]
		if now.Before(j.NotBefore) || !now.Before(j.Deadline) {
			continue
		}
		r.Status = RunSent
		t := now
		r.SentAt = &t
		c := *j
		c.Vars = copyVars(r.Vars)
		out = append(out, c)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].ID < out[k].ID })
	return out, nil
}

func (m *Memory) FinishRun(ctx context.Context, jobID int64, deviceID, status string, exitCode int, output string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.j().runs[jobID][deviceID]
	if !ok || r.Status != RunSent {
		return ErrNotFound
	}
	r.Status = status
	ec := exitCode
	r.ExitCode = &ec
	r.Output = output
	t := now
	r.FinishedAt = &t
	return nil
}

// sentGrace is how long a sent run may take beyond the job deadline plus its timeout.
const sentGrace = 5 * time.Minute

func (m *Memory) ExpireRuns(ctx context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, runs := range m.j().runs {
		j := m.j().jobs[id]
		for _, r := range runs {
			expire := (r.Status == RunQueued && !now.Before(j.Deadline)) ||
				(r.Status == RunSent && !now.Before(j.Deadline.Add(time.Duration(j.TimeoutSec)*time.Second+sentGrace)))
			if expire {
				r.Status = RunExpired
				t := now
				r.FinishedAt = &t
			}
		}
	}
	return nil
}

func copyVars(v Vars) Vars {
	if len(v) == 0 {
		return nil
	}
	c := make(Vars, len(v))
	for k, x := range v {
		c[k] = x
	}
	return c
}
