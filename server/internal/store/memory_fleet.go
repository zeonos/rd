package store

import (
	"context"
	"sort"
	"time"
)

type memFleet struct {
	inventory map[string]string
	releases  map[int]*Release
}

func (m *Memory) f() *memFleet {
	if m.fleet == nil {
		m.fleet = &memFleet{inventory: map[string]string{}, releases: map[int]*Release{}}
	}
	return m.fleet
}

func (m *Memory) SetDeviceIP(ctx context.Context, id, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return ErrNotFound
	}
	d.IP = ip
	return nil
}

func (m *Memory) SetDeviceInventory(ctx context.Context, id, inventory, disks string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return ErrNotFound
	}
	t := at
	d.InventoryAt = &t
	d.Disks = disks
	m.f().inventory[id] = inventory
	return nil
}

func (m *Memory) GetDeviceInventory(ctx context.Context, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.devices[id]; !ok {
		return "", ErrNotFound
	}
	return m.f().inventory[id], nil
}

func (m *Memory) AddRelease(ctx context.Context, r *Release) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.f().releases[r.Build]; ok {
		return ErrConflict
	}
	r.CreatedAt = m.nowSource()
	c := *r
	c.Current = false
	m.f().releases[r.Build] = &c
	return nil
}

func (m *Memory) ListReleases(ctx context.Context) ([]Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Release, 0, len(m.f().releases))
	for _, r := range m.f().releases {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Build > out[j].Build })
	return out, nil
}

func (m *Memory) SetCurrentRelease(ctx context.Context, build int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if build != 0 {
		if _, ok := m.f().releases[build]; !ok {
			return ErrNotFound
		}
	}
	for b, r := range m.f().releases {
		r.Current = b == build
	}
	return nil
}

func (m *Memory) DeleteRelease(ctx context.Context, build int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.f().releases[build]; !ok {
		return ErrNotFound
	}
	delete(m.f().releases, build)
	return nil
}

func (m *Memory) HasRecentJob(ctx context.Context, deviceID, name string, since time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, j := range m.j().jobs {
		if j.Name != name || j.CreatedAt.Before(since) {
			continue
		}
		if _, ok := m.j().runs[id][deviceID]; ok {
			return true, nil
		}
	}
	return false, nil
}
