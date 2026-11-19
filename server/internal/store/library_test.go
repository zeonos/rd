package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLibrary(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if p, ok := s.(*Postgres); ok {
				for _, tbl := range []string{"schedules", "job_runs", "jobs", "scripts"} {
					p.pool.Exec(ctx, "DELETE FROM "+tbl)
				}
				p.pool.Exec(ctx, "DELETE FROM devices WHERE id IN ('l1','l2')")
			}
			now := time.Now().UTC().Truncate(time.Second)
			for _, id := range []string{"l1", "l2"} {
				if err := s.EnrollDevice(ctx, &Device{ID: id, UUID: "u" + id}); err != nil {
					t.Fatal(err)
				}
			}
			g, err := s.EnsureGroupPath(ctx, "Lib/Site")
			if err != nil {
				t.Fatal(err)
			}

			// variables
			if err := s.SetDeviceVars(ctx, "l1", Vars{"IP": "10.0.0.1", "ROLE": "primary"}); err != nil {
				t.Fatal(err)
			}
			if err := s.SetGroupVars(ctx, g.ID, Vars{"ROLE": "replica"}); err != nil {
				t.Fatal(err)
			}
			if err := s.SetDeviceVars(ctx, "nope", Vars{"A": "b"}); !errors.Is(err, ErrNotFound) {
				t.Fatal("vars on unknown device:", err)
			}
			dv, gv, err := s.AllVars(ctx)
			if err != nil || dv["l1"]["IP"] != "10.0.0.1" || gv[g.ID]["ROLE"] != "replica" || dv["l2"] != nil {
				t.Fatalf("vars: %v %v %v", dv, gv, err)
			}
			s.SetDeviceVars(ctx, "l1", Vars{})
			if dv, _, _ = s.AllVars(ctx); dv["l1"] != nil {
				t.Fatal("cleared vars still listed")
			}

			// script params round-trip
			sc := &Script{Name: "Ping", Shell: "powershell", Body: "ping $IP", RunAs: "system", TimeoutSec: 60,
				Params: []ScriptParam{{Name: "IP", Default: "127.0.0.1", Description: "Target"}}}
			if err := s.SaveScript(ctx, sc); err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetScript(ctx, sc.ID)
			if len(got.Params) != 1 || got.Params[0].Default != "127.0.0.1" {
				t.Fatalf("params: %+v", got.Params)
			}

			// jobs with per-run vars, linked to the script
			sid := sc.ID
			j := &Job{Kind: "script", Name: "Ping", Shell: "powershell", Body: "ping $IP", RunAs: "system", TimeoutSec: 60,
				CreatedBy: "t", NotBefore: now, Deadline: now.Add(time.Hour), ScriptID: &sid,
				RunVars: map[string]Vars{"l1": {"IP": "10.0.0.1"}, "l2": {"IP": "10.0.0.2"}}}
			if err := s.CreateJob(ctx, j, []string{"l1", "l2"}); err != nil {
				t.Fatal(err)
			}
			other := &Job{Kind: "power", Name: "Reboot", Action: "reboot", RunAs: "system", TimeoutSec: 60, CreatedBy: "t",
				NotBefore: now, Deadline: now.Add(time.Hour)}
			s.CreateJob(ctx, other, []string{"l1"})
			claimed, err := s.ClaimRuns(ctx, "l2", now)
			if err != nil || len(claimed) != 1 || claimed[0].Vars["IP"] != "10.0.0.2" || claimed[0].ScriptID == nil {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			_, runs, _ := s.GetJob(ctx, j.ID)
			if len(runs) != 2 || runs[0].Vars["IP"] != "10.0.0.1" {
				t.Fatalf("runs: %+v", runs)
			}
			byScript, _ := s.ListJobs(ctx, JobFilter{ScriptID: sc.ID})
			if len(byScript) != 1 || byScript[0].ID != j.ID || byScript[0].Counts[RunQueued] != 1 {
				t.Fatalf("jobs by script: %+v", byScript)
			}
			hist, err := s.ListDeviceRuns(ctx, "l1", 10)
			if err != nil || len(hist) != 2 || hist[0].JobID != other.ID || hist[1].JobName != "Ping" || hist[1].Vars["IP"] != "10.0.0.1" {
				t.Fatalf("device runs: %+v %v", hist, err)
			}

			// schedules
			sch := &Schedule{ScriptID: sc.ID, Kind: "days", AtMinute: 120, Weekdays: 0b0111110, TZ: "Europe/Copenhagen",
				Target: JobTarget{Groups: []int64{g.ID}}, TargetText: "Lib/Site", Vars: Vars{"IP": "1.1.1.1"},
				DeadlineHours: 2, Enabled: true, NextRun: now.Add(time.Hour), CreatedBy: "t"}
			if err := s.SaveSchedule(ctx, sch); err != nil || sch.ID == 0 || sch.ScriptName != "Ping" {
				t.Fatalf("save schedule: %v %+v", err, sch)
			}
			if err := s.SaveSchedule(ctx, &Schedule{ScriptID: 999999, Kind: "hours", EveryHours: 1, TZ: "UTC", NextRun: now, CreatedBy: "t"}); !errors.Is(err, ErrNotFound) {
				t.Fatal("schedule for unknown script:", err)
			}
			if due, _ := s.DueSchedules(ctx, now); len(due) != 0 {
				t.Fatal("not yet due")
			}
			due, err := s.DueSchedules(ctx, now.Add(time.Hour))
			if err != nil || len(due) != 1 || due[0].Target.Groups[0] != g.ID || due[0].Vars["IP"] != "1.1.1.1" {
				t.Fatalf("due: %+v %v", due, err)
			}
			jid := j.ID
			if err := s.MarkScheduleRun(ctx, sch.ID, now.Add(time.Hour), &jid, now.Add(25*time.Hour)); err != nil {
				t.Fatal(err)
			}
			gs, _ := s.GetSchedule(ctx, sch.ID)
			if gs.LastJobID == nil || *gs.LastJobID != jid || !gs.NextRun.Equal(now.Add(25*time.Hour)) {
				t.Fatalf("after run: %+v", gs)
			}
			sch.Enabled = false
			if err := s.SaveSchedule(ctx, sch); err != nil || sch.LastJobID == nil {
				t.Fatalf("update schedule: %v %+v", err, sch)
			}
			if due, _ = s.DueSchedules(ctx, now.Add(48*time.Hour)); len(due) != 0 {
				t.Fatal("disabled schedule due")
			}
			list, _ := s.ListSchedules(ctx)
			if len(list) != 1 {
				t.Fatalf("schedules: %+v", list)
			}
			// deleting the script removes its schedules
			if err := s.DeleteScript(ctx, sc.ID); err != nil {
				t.Fatal(err)
			}
			if list, _ = s.ListSchedules(ctx); len(list) != 0 {
				t.Fatal("schedule outlived its script")
			}
		})
	}
}
