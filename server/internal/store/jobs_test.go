package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestJobs(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if p, ok := s.(*Postgres); ok {
				for _, tbl := range []string{"job_runs", "jobs", "scripts", "server_keys"} {
					p.pool.Exec(ctx, "DELETE FROM "+tbl)
				}
			}
			now := time.Now().UTC().Truncate(time.Second)

			// server keys
			v, err := s.PutServerKeyIfAbsent(ctx, "k", "one")
			if err != nil || v != "one" {
				t.Fatal(err, v)
			}
			if v, _ = s.PutServerKeyIfAbsent(ctx, "k", "two"); v != "one" {
				t.Fatal("server key overwritten")
			}

			// scripts
			sc := &Script{Name: "Disk space", Shell: "powershell", Body: "Get-PSDrive C", RunAs: "system", TimeoutSec: 60}
			if err := s.SaveScript(ctx, sc); err != nil || sc.ID == 0 {
				t.Fatal(err)
			}
			if err := s.SaveScript(ctx, &Script{Name: "disk SPACE", Shell: "cmd", Body: "x", RunAs: "system", TimeoutSec: 1}); !errors.Is(err, ErrConflict) {
				t.Fatalf("duplicate script name: %v", err)
			}
			sc.Body = "Get-PSDrive"
			if err := s.SaveScript(ctx, sc); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.GetScript(ctx, sc.ID); got.Body != "Get-PSDrive" {
				t.Fatal("script not updated")
			}

			// jobs
			j := &Job{Kind: "script", Name: "Disk space", Shell: "powershell", Body: "x", RunAs: "system", TimeoutSec: 60,
				Target: "Office", CreatedBy: "admin", NotBefore: now, Deadline: now.Add(time.Hour)}
			if err := s.CreateJob(ctx, j, []string{"d1", "d2"}); err != nil {
				t.Fatal(err)
			}
			later := &Job{Kind: "power", Name: "Reboot", Action: "reboot", RunAs: "system", TimeoutSec: 60,
				CreatedBy: "admin", NotBefore: now.Add(30 * time.Minute), Deadline: now.Add(2 * time.Hour)}
			if err := s.CreateJob(ctx, later, []string{"d1"}); err != nil {
				t.Fatal(err)
			}
			got, err := s.ClaimRuns(ctx, "d1", now)
			if err != nil || len(got) != 1 || got[0].ID != j.ID {
				t.Fatalf("claim: %v %+v", err, got)
			}
			if again, _ := s.ClaimRuns(ctx, "d1", now); len(again) != 0 {
				t.Fatal("claimed twice")
			}
			if err := s.FinishRun(ctx, j.ID, "d1", RunSuccess, 0, "ok", now); err != nil {
				t.Fatal(err)
			}
			if err := s.FinishRun(ctx, j.ID, "d1", RunSuccess, 0, "ok", now); !errors.Is(err, ErrNotFound) {
				t.Fatalf("finished twice: %v", err)
			}
			if err := s.FinishRun(ctx, j.ID, "d2", RunSuccess, 0, "", now); !errors.Is(err, ErrNotFound) {
				t.Fatal("finished a run that was never sent")
			}
			// scheduled job becomes due later
			if due, _ := s.ClaimRuns(ctx, "d1", now.Add(31*time.Minute)); len(due) != 1 || due[0].Action != "reboot" {
				t.Fatalf("scheduled job: %+v", due)
			}
			if err := s.CancelJob(ctx, j.ID); err != nil {
				t.Fatal(err)
			}
			_, runs, err := s.GetJob(ctx, j.ID)
			if err != nil || len(runs) != 2 || runs[0].Status != RunSuccess || runs[1].Status != RunCancelled || *runs[0].ExitCode != 0 {
				t.Fatalf("runs: %v %+v", err, runs)
			}
			// a sent run that never reports expires after deadline + timeout + grace
			if err := s.ExpireRuns(ctx, now.Add(2*time.Hour+6*time.Minute+time.Minute)); err != nil {
				t.Fatal(err)
			}
			_, runs, _ = s.GetJob(ctx, later.ID)
			if runs[0].Status != RunExpired {
				t.Fatalf("expected expired, got %s", runs[0].Status)
			}
			list, err := s.ListJobs(ctx, JobFilter{Limit: 10})
			if err != nil || len(list) != 2 || list[0].ID != later.ID || list[1].Counts[RunSuccess] != 1 {
				t.Fatalf("list jobs: %v %+v", err, list)
			}
		})
	}
}
