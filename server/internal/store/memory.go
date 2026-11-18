package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is an in-memory Store for tests and local experiments.
type Memory struct {
	mu        sync.Mutex
	users     map[int64]*User
	sessions  map[string]*Session
	groups    map[int64]*Group
	devices   map[string]*Device
	tokens    map[int64]*EnrollToken
	policies  map[int64]map[string]string
	jobState  *memJobs
	nextID    int64
	nowSource func() time.Time
}

func NewMemory() *Memory {
	return &Memory{
		users:     map[int64]*User{},
		sessions:  map[string]*Session{},
		groups:    map[int64]*Group{},
		devices:   map[string]*Device{},
		tokens:    map[int64]*EnrollToken{},
		policies:  map[int64]map[string]string{},
		nowSource: time.Now,
	}
}

func (m *Memory) Close() {}

func (m *Memory) id() int64 { m.nextID++; return m.nextID }

// --- users ---

func (m *Memory) CountUsers(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users), nil
}

func (m *Memory) CreateUser(ctx context.Context, u *User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.users {
		if strings.EqualFold(x.Name, u.Name) {
			return ErrConflict
		}
	}
	u.ID = m.id()
	u.CreatedAt = m.nowSource()
	c := *u
	m.users[u.ID] = &c
	return nil
}

func (m *Memory) GetUserByName(ctx context.Context, name string) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.users {
		if strings.EqualFold(x.Name, name) {
			c := *x
			return &c, nil
		}
	}
	return nil, ErrNotFound
}

func (m *Memory) GetUserByID(ctx context.Context, id int64) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := *x
	return &c, nil
}

func (m *Memory) SetUserPassword(ctx context.Context, id int64, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.users[id]
	if !ok {
		return ErrNotFound
	}
	x.PasswordHash = hash
	return nil
}

func (m *Memory) SetUserTOTP(ctx context.Context, id int64, secret string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.users[id]
	if !ok {
		return ErrNotFound
	}
	x.TOTPSecret, x.TOTPEnabled, x.TOTPLastStep = secret, enabled, 0
	return nil
}

func (m *Memory) AdvanceTOTPStep(ctx context.Context, id int64, step int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.users[id]
	if !ok {
		return false, ErrNotFound
	}
	if step <= x.TOTPLastStep {
		return false, nil
	}
	x.TOTPLastStep = step
	return true, nil
}

// --- sessions ---

func (m *Memory) CreateSession(ctx context.Context, s *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *s
	m.sessions[s.TokenHash] = &c
	return nil
}

func (m *Memory) GetSession(ctx context.Context, h string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[h]
	if !ok {
		return nil, ErrNotFound
	}
	c := *s
	return &c, nil
}

func (m *Memory) TouchSession(ctx context.Context, h string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[h]; ok {
		s.LastSeen = at
	}
	return nil
}

func (m *Memory) DeleteSession(ctx context.Context, h string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, h)
	return nil
}

func (m *Memory) DeleteUserSessions(ctx context.Context, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, s := range m.sessions {
		if s.UserID == userID {
			delete(m.sessions, k)
		}
	}
	return nil
}

func (m *Memory) DeleteExpiredSessions(ctx context.Context, now time.Time, idle time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, s := range m.sessions {
		if !now.Before(s.ExpiresAt) || now.Sub(s.LastSeen) > idle {
			delete(m.sessions, k)
		}
	}
	return nil
}

// --- groups ---

func (m *Memory) pathOf(g *Group) string {
	parts := []string{g.Name}
	for p := g.ParentID; p != nil; {
		pg, ok := m.groups[*p]
		if !ok {
			break
		}
		parts = append([]string{pg.Name}, parts...)
		p = pg.ParentID
	}
	return JoinPath(parts)
}

func (m *Memory) ListGroups(ctx context.Context) ([]Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Group, 0, len(m.groups))
	for _, g := range m.groups {
		c := *g
		c.Path = m.pathOf(g)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out, nil
}

func (m *Memory) GetGroup(ctx context.Context, id int64) (*Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := *g
	c.Path = m.pathOf(g)
	return &c, nil
}

func sameParent(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (m *Memory) child(parent *int64, name string) *Group {
	for _, g := range m.groups {
		if sameParent(g.ParentID, parent) && strings.EqualFold(g.Name, name) {
			return g
		}
	}
	return nil
}

func (m *Memory) EnsureGroupPath(ctx context.Context, path string) (*Group, error) {
	parts := SplitPath(path)
	if len(parts) == 0 {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var parent *int64
	var g *Group
	for _, name := range parts {
		g = m.child(parent, name)
		if g == nil {
			g = &Group{ID: m.id(), Name: name, ParentID: parent}
			m.groups[g.ID] = g
		}
		id := g.ID
		parent = &id
	}
	c := *g
	c.Path = m.pathOf(g)
	return &c, nil
}

func (m *Memory) FindGroupPath(ctx context.Context, path string) (*Group, error) {
	parts := SplitPath(path)
	if len(parts) == 0 {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var parent *int64
	var g *Group
	for _, name := range parts {
		g = m.child(parent, name)
		if g == nil {
			return nil, ErrNotFound
		}
		id := g.ID
		parent = &id
	}
	c := *g
	c.Path = m.pathOf(g)
	return &c, nil
}

func (m *Memory) RenameGroup(ctx context.Context, id int64, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[id]
	if !ok {
		return ErrNotFound
	}
	if o := m.child(g.ParentID, name); o != nil && o.ID != id {
		return ErrConflict
	}
	g.Name = name
	return nil
}

func (m *Memory) DeleteGroup(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.groups[id]; !ok {
		return ErrNotFound
	}
	for _, g := range m.groups {
		if g.ParentID != nil && *g.ParentID == id {
			return ErrConflict
		}
	}
	for _, d := range m.devices {
		if d.GroupID != nil && *d.GroupID == id {
			return ErrConflict
		}
	}
	for _, t := range m.tokens {
		if t.GroupID != nil && *t.GroupID == id {
			return ErrConflict
		}
	}
	delete(m.groups, id)
	delete(m.policies, id)
	return nil
}

// --- policies ---

func (m *Memory) ListPolicies(ctx context.Context) (map[int64]map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[int64]map[string]string, len(m.policies))
	for k, v := range m.policies {
		c := make(map[string]string, len(v))
		for kk, vv := range v {
			c[kk] = vv
		}
		out[k] = c
	}
	return out, nil
}

func (m *Memory) SetPolicy(ctx context.Context, scope int64, values map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if scope != AccountScope {
		if _, ok := m.groups[scope]; !ok {
			return ErrNotFound
		}
	}
	if len(values) == 0 {
		delete(m.policies, scope)
		return nil
	}
	c := make(map[string]string, len(values))
	for k, v := range values {
		c[k] = v
	}
	m.policies[scope] = c
	return nil
}

// --- devices ---

func (m *Memory) fillDevice(d *Device) Device {
	c := *d
	if d.GroupID != nil {
		if g, ok := m.groups[*d.GroupID]; ok {
			c.GroupPath = m.pathOf(g)
		}
	}
	return c
}

func (m *Memory) ListDevices(ctx context.Context) ([]Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Device, 0, len(m.devices))
	for _, d := range m.devices {
		out = append(out, m.fillDevice(d))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) GetDevice(ctx context.Context, id string) (*Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := m.fillDevice(d)
	return &c, nil
}

func (m *Memory) EnrollDevice(ctx context.Context, d *Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.nowSource()
	if old, ok := m.devices[d.ID]; ok {
		if old.UUID != d.UUID {
			// new installation: old credentials are meaningless
			old.PasswordH1, old.PublicKey = "", ""
		}
		old.UUID = d.UUID
		old.GroupID = d.GroupID
		if d.Alias != "" {
			old.Alias = d.Alias
		}
		if d.Note != "" {
			old.Note = d.Note
		}
		old.UpdatedAt = now
		return nil
	}
	c := *d
	c.CreatedAt, c.UpdatedAt = now, now
	m.devices[d.ID] = &c
	return nil
}

func (m *Memory) UpdateSysinfo(ctx context.Context, id string, u SysinfoUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return ErrNotFound
	}
	d.Hostname, d.Username, d.OS, d.CPU, d.Memory, d.Version = u.Hostname, u.Username, u.OS, u.CPU, u.Memory, u.Version
	if u.PasswordH1 != "" {
		d.PasswordH1 = u.PasswordH1
	}
	d.UpdatedAt = m.nowSource()
	return nil
}

func (m *Memory) Heartbeat(ctx context.Context, id string, conns int, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return ErrNotFound
	}
	t := at
	d.LastSeen = &t
	d.Conns = conns
	return nil
}

func (m *Memory) UpdateDevice(ctx context.Context, id string, alias, note *string, groupID **int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return ErrNotFound
	}
	if alias != nil {
		d.Alias = *alias
	}
	if note != nil {
		d.Note = *note
	}
	if groupID != nil {
		if *groupID != nil {
			if _, ok := m.groups[**groupID]; !ok {
				return ErrNotFound
			}
		}
		d.GroupID = *groupID
	}
	d.UpdatedAt = m.nowSource()
	return nil
}

func (m *Memory) DeleteDevice(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.devices[id]; !ok {
		return ErrNotFound
	}
	delete(m.devices, id)
	return nil
}

// --- enrolment tokens ---

func (m *Memory) fillToken(t *EnrollToken) EnrollToken {
	c := *t
	if t.GroupID != nil {
		if g, ok := m.groups[*t.GroupID]; ok {
			c.GroupPath = m.pathOf(g)
		}
	}
	return c
}

func (m *Memory) CreateEnrollToken(ctx context.Context, t *EnrollToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t.ID = m.id()
	t.CreatedAt = m.nowSource()
	c := *t
	m.tokens[t.ID] = &c
	return nil
}

func (m *Memory) ListEnrollTokens(ctx context.Context) ([]EnrollToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]EnrollToken, 0, len(m.tokens))
	for _, t := range m.tokens {
		out = append(out, m.fillToken(t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) UseEnrollToken(ctx context.Context, h string, now time.Time) (*EnrollToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if t.TokenHash == h {
			if !t.Usable(now) {
				return nil, ErrTokenUnusable
			}
			t.Uses++
			c := m.fillToken(t)
			return &c, nil
		}
	}
	return nil, ErrNotFound
}

func (m *Memory) DeleteEnrollToken(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tokens[id]; !ok {
		return ErrNotFound
	}
	delete(m.tokens, id)
	return nil
}
