package api

import (
	"context"
	"fmt"
	"time"
)

// checkOffline notifies once when a device has been silent for OfflineAlert
// and once when it comes back. State is kept in memory: after a restart,
// devices already offline are not reported again.
func (s *Server) checkOffline(ctx context.Context, now time.Time) {
	if s.cfg.OfflineAlert <= 0 {
		return
	}
	devices, err := s.store.ListDevices(ctx)
	if err != nil {
		s.cfg.Logger.Error("offline check", "err", err)
		return
	}
	first := s.offline == nil
	if first {
		s.offline = map[string]bool{}
	}
	seen := map[string]bool{}
	for i := range devices {
		d := &devices[i]
		seen[d.ID] = true
		if d.LastSeen == nil {
			continue // never came online
		}
		name := d.Alias
		if name == "" {
			name = d.Hostname
		}
		if name == "" {
			name = d.ID
		}
		down := now.Sub(*d.LastSeen) >= s.cfg.OfflineAlert
		switch {
		case down && !s.offline[d.ID]:
			s.offline[d.ID] = true
			if !first {
				s.notify("RD: device offline", fmt.Sprintf("%s (%s, %s) has been offline since %s",
					name, groupName(d), d.ID, d.LastSeen.Local().Format("15:04 02 Jan")))
			}
		case !down && s.offline[d.ID]:
			delete(s.offline, d.ID)
			s.notify("RD: device back online", fmt.Sprintf("%s (%s, %s) is online again", name, groupName(d), d.ID))
		}
	}
	for id := range s.offline {
		if !seen[id] {
			delete(s.offline, id)
		}
	}
}
