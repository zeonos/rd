package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

func (p *Postgres) AllVars(ctx context.Context) (map[string]Vars, map[int64]Vars, error) {
	dv := map[string]Vars{}
	rows, err := p.pool.Query(ctx, `SELECT id, vars FROM devices WHERE vars <> '{}'::jsonb`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		var b []byte
		if err := rows.Scan(&id, &b); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if v := decodeVars(b); v != nil {
			dv[id] = v
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	gv := map[int64]Vars{}
	rows, err = p.pool.Query(ctx, `SELECT id, vars FROM device_groups WHERE vars <> '{}'::jsonb`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var b []byte
		if err := rows.Scan(&id, &b); err != nil {
			return nil, nil, err
		}
		if v := decodeVars(b); v != nil {
			gv[id] = v
		}
	}
	return dv, gv, rows.Err()
}

func varsArg(v Vars) string {
	if v == nil {
		v = Vars{}
	}
	return jsonArg(v)
}

func (p *Postgres) SetDeviceVars(ctx context.Context, id string, v Vars) error {
	return affected(p.pool.Exec(ctx, `UPDATE devices SET vars=$2::jsonb WHERE id=$1`, id, varsArg(v)))
}

func (p *Postgres) SetGroupVars(ctx context.Context, id int64, v Vars) error {
	return affected(p.pool.Exec(ctx, `UPDATE device_groups SET vars=$2::jsonb WHERE id=$1`, id, varsArg(v)))
}

func (p *Postgres) ListDeviceRuns(ctx context.Context, deviceID string, limit int) ([]DeviceRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx,
		`SELECT r.job_id, r.device_id, r.status, r.exit_code, r.output, r.sent_at, r.finished_at, r.vars,
		        j.name, j.kind, j.script_id, j.created_at, j.created_by
		 FROM job_runs r JOIN jobs j ON j.id = r.job_id
		 WHERE r.device_id=$1 ORDER BY r.job_id DESC LIMIT $2`, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceRun
	for rows.Next() {
		var d DeviceRun
		var vars []byte
		if err := rows.Scan(&d.JobID, &d.DeviceID, &d.Status, &d.ExitCode, &d.Output, &d.SentAt, &d.FinishedAt, &vars,
			&d.JobName, &d.Kind, &d.ScriptID, &d.CreatedAt, &d.CreatedBy); err != nil {
			return nil, err
		}
		d.Vars = decodeVars(vars)
		out = append(out, d)
	}
	return out, rows.Err()
}

const scheduleCols = `s.id, s.script_id, sc.name, s.kind, s.every_hours, s.at_minute, s.weekdays, s.tz, s.target, s.target_text,
	s.vars, s.deadline_hours, s.enabled, s.next_run, s.last_run, s.last_job_id, s.created_by, s.created_at`

const scheduleFrom = ` FROM schedules s JOIN scripts sc ON sc.id = s.script_id`

func scanSchedule(row pgx.Row) (*Schedule, error) {
	var s Schedule
	var target, vars []byte
	err := row.Scan(&s.ID, &s.ScriptID, &s.ScriptName, &s.Kind, &s.EveryHours, &s.AtMinute, &s.Weekdays, &s.TZ, &target, &s.TargetText,
		&vars, &s.DeadlineHours, &s.Enabled, &s.NextRun, &s.LastRun, &s.LastJobID, &s.CreatedBy, &s.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	json.Unmarshal(target, &s.Target)
	s.Vars = decodeVars(vars)
	return &s, nil
}

func (p *Postgres) querySchedules(ctx context.Context, where string, args ...any) ([]Schedule, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+scheduleCols+scheduleFrom+` `+where+` ORDER BY s.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (p *Postgres) ListSchedules(ctx context.Context) ([]Schedule, error) {
	return p.querySchedules(ctx, "")
}

func (p *Postgres) GetSchedule(ctx context.Context, id int64) (*Schedule, error) {
	return scanSchedule(p.pool.QueryRow(ctx, `SELECT `+scheduleCols+scheduleFrom+` WHERE s.id=$1`, id))
}

func (p *Postgres) SaveSchedule(ctx context.Context, s *Schedule) error {
	target := jsonArg(s.Target)
	var err error
	if s.ID == 0 {
		err = p.pool.QueryRow(ctx,
			`INSERT INTO schedules(script_id, kind, every_hours, at_minute, weekdays, tz, target, target_text, vars, deadline_hours, enabled, next_run, created_by)
			 VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9::jsonb,$10,$11,$12,$13) RETURNING id, created_at`,
			s.ScriptID, s.Kind, s.EveryHours, s.AtMinute, s.Weekdays, s.TZ, target, s.TargetText, varsArg(s.Vars),
			s.DeadlineHours, s.Enabled, s.NextRun, s.CreatedBy).Scan(&s.ID, &s.CreatedAt)
		if isFKViolation(err) {
			return ErrNotFound
		}
	} else {
		err = notFound(p.pool.QueryRow(ctx,
			`UPDATE schedules SET script_id=$2, kind=$3, every_hours=$4, at_minute=$5, weekdays=$6, tz=$7, target=$8::jsonb,
			   target_text=$9, vars=$10::jsonb, deadline_hours=$11, enabled=$12, next_run=$13
			 WHERE id=$1 RETURNING created_at, created_by, last_run, last_job_id`,
			s.ID, s.ScriptID, s.Kind, s.EveryHours, s.AtMinute, s.Weekdays, s.TZ, target, s.TargetText, varsArg(s.Vars),
			s.DeadlineHours, s.Enabled, s.NextRun).Scan(&s.CreatedAt, &s.CreatedBy, &s.LastRun, &s.LastJobID))
		if isFKViolation(err) {
			return ErrNotFound
		}
	}
	if err != nil {
		return err
	}
	return p.pool.QueryRow(ctx, `SELECT name FROM scripts WHERE id=$1`, s.ScriptID).Scan(&s.ScriptName)
}

func (p *Postgres) DeleteSchedule(ctx context.Context, id int64) error {
	return affected(p.pool.Exec(ctx, `DELETE FROM schedules WHERE id=$1`, id))
}

func (p *Postgres) DueSchedules(ctx context.Context, now time.Time) ([]Schedule, error) {
	return p.querySchedules(ctx, `WHERE s.enabled AND s.next_run <= $1`, now)
}

func (p *Postgres) MarkScheduleRun(ctx context.Context, id int64, ranAt time.Time, jobID *int64, next time.Time) error {
	return affected(p.pool.Exec(ctx,
		`UPDATE schedules SET last_run=$2, last_job_id=COALESCE($3, last_job_id), next_run=$4 WHERE id=$1`,
		id, ranAt, jobID, next))
}
