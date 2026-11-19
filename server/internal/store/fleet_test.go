package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFleet(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if p, ok := s.(*Postgres); ok {
				for _, tbl := range []string{"job_runs", "jobs", "releases"} {
					p.pool.Exec(ctx, "DELETE FROM "+tbl)
				}
				p.pool.Exec(ctx, "DELETE FROM devices WHERE id IN ('f1','f2')")
			}
			now := time.Now().UTC().Truncate(time.Second)
			if err := s.EnrollDevice(ctx, &Device{ID: "f1", UUID: "u"}); err != nil {
				t.Fatal(err)
			}

			// IP, build, inventory
			if err := s.SetDeviceIP(ctx, "f1", "203.0.113.9"); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateSysinfo(ctx, "f1", SysinfoUpdate{Hostname: "H", Build: 12}); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateSysinfo(ctx, "f1", SysinfoUpdate{Hostname: "H"}); err != nil { // 0 keeps the build
				t.Fatal(err)
			}
			if err := s.SetDeviceInventory(ctx, "f1", `{"disks":[]}`, `[]`, now); err != nil {
				t.Fatal(err)
			}
			d, err := s.GetDevice(ctx, "f1")
			if err != nil || d.IP != "203.0.113.9" || d.Build != 12 || d.InventoryAt == nil || !d.InventoryAt.Equal(now) || d.Disks != `[]` {
				t.Fatalf("device: %+v %v", d, err)
			}
			if inv, err := s.GetDeviceInventory(ctx, "f1"); err != nil || inv != `{"disks":[]}` {
				t.Fatal(inv, err)
			}
			if err := s.SetDeviceIP(ctx, "nope", "x"); !errors.Is(err, ErrNotFound) {
				t.Fatal("ip for unknown device:", err)
			}

			// releases: at most one current
			for _, b := range []int{3, 4} {
				if err := s.AddRelease(ctx, &Release{Build: b, SHA256: "s", Size: 1}); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.AddRelease(ctx, &Release{Build: 3, SHA256: "s", Size: 1}); !errors.Is(err, ErrConflict) {
				t.Fatal("duplicate release:", err)
			}
			s.SetCurrentRelease(ctx, 3)
			if err := s.SetCurrentRelease(ctx, 4); err != nil {
				t.Fatal(err)
			}
			rels, _ := s.ListReleases(ctx)
			if len(rels) != 2 || rels[0].Build != 4 || !rels[0].Current || rels[1].Current {
				t.Fatalf("releases: %+v", rels)
			}
			if err := s.SetCurrentRelease(ctx, 99); !errors.Is(err, ErrNotFound) {
				t.Fatal("current unknown release:", err)
			}
			s.SetCurrentRelease(ctx, 0)
			if err := s.DeleteRelease(ctx, 3); err != nil {
				t.Fatal(err)
			}

			// recent jobs by name and device
			j := &Job{Kind: "update", Name: "Update to build 4", CreatedBy: "t", NotBefore: now, Deadline: now.Add(time.Hour)}
			if err := s.CreateJob(ctx, j, []string{"f1"}); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.HasRecentJob(ctx, "f1", "Update to build 4", now.Add(-time.Hour)); err != nil || !ok {
				t.Fatal("recent job not found", err)
			}
			if ok, _ := s.HasRecentJob(ctx, "f2", "Update to build 4", now.Add(-time.Hour)); ok {
				t.Fatal("job found for another device")
			}
			if ok, _ := s.HasRecentJob(ctx, "f1", "Update to build 4", now.Add(time.Hour)); ok {
				t.Fatal("old job counted as recent")
			}
		})
	}
}
