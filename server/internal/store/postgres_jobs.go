package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (p *Postgres) SetDevicePublicKey(ctx context.Context, id, key string) error {
	return affected(p.pool.Exec(ctx, `UPDATE devices SET public_key=$2 WHERE id=$1`, id, key))
}

func (p *Postgres) GetServerKey(ctx context.Context, name string) (string, error) {
	var v string
	err := p.pool.QueryRow(ctx, `SELECT value FROM server_keys WHERE name=$1`, name).Scan(&v)
	return v, notFound(err)
}

func (p *Postgres) PutServerKeyIfAbsent(ctx context.Context, name, value string) (string, error) {
	if _, err := p.pool.Exec(ctx, `INSERT INTO server_keys(name, value) VALUES($1,$2) ON CONFLICT DO NOTHING`, name, value); err != nil {
		return "", err
	}
	return p.GetServerKey(ctx, name)
}

const scriptCols = `id, name, description, shell, body, run_as, timeout_sec, created_at, updated_at`

func scanScript(row pgx.Row) (*Script, error) {
	var s Script
	err := row.Scan(&s.ID, &s.Name, &s.Description, &s.Shell, &s.Body, &s.RunAs, &s.TimeoutSec, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &s, nil
}

func (p *Postgres) ListScripts(ctx context.Context) ([]Script, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+scriptCols+` FROM scripts ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Script
	for rows.Next() {
		s, err := scanScript(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (p *Postgres) GetScript(ctx context.Context, id int64) (*Script, error) {
	return scanScript(p.pool.QueryRow(ctx, `SELECT `+scriptCols+` FROM scripts WHERE id=$1`, id))
}

func (p *Postgres) SaveScript(ctx context.Context, s *Script) error {
	var err error
	if s.ID == 0 {
		err = p.pool.QueryRow(ctx,
			`INSERT INTO scripts(name, description, shell, body, run_as, timeout_sec) VALUES($1,$2,$3,$4,$5,$6)
			 RETURNING id, created_at, updated_at`,
			s.Name, s.Description, s.Shell, s.Body, s.RunAs, s.TimeoutSec).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
	} else {
		err = p.pool.QueryRow(ctx,
			`UPDATE scripts SET name=$2, description=$3, shell=$4, body=$5, run_as=$6, timeout_sec=$7, updated_at=now()
			 WHERE id=$1 RETURNING created_at, updated_at`,
			s.ID, s.Name, s.Description, s.Shell, s.Body, s.RunAs, s.TimeoutSec).Scan(&s.CreatedAt, &s.UpdatedAt)
		err = notFound(err)
	}
	if isUnique(err) {
		return ErrConflict
	}
	return err
}

func (p *Postgres) DeleteScript(ctx context.Context, id int64) error {
	return affected(p.pool.Exec(ctx, `DELETE FROM scripts WHERE id=$1`, id))
}

const jobCols = `id, kind, name, shell, body, run_as, timeout_sec, action, target, created_by, not_before, deadline, created_at`

func scanJob(row pgx.Row) (*Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Kind, &j.Name, &j.Shell, &j.Body, &j.RunAs, &j.TimeoutSec, &j.Action, &j.Target,
		&j.CreatedBy, &j.NotBefore, &j.Deadline, &j.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &j, nil
}

func (p *Postgres) CreateJob(ctx context.Context, j *Job, deviceIDs []string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx,
		`INSERT INTO jobs(kind, name, shell, body, run_as, timeout_sec, action, target, created_by, not_before, deadline)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id, created_at`,
		j.Kind, j.Name, j.Shell, j.Body, j.RunAs, j.TimeoutSec, j.Action, j.Target, j.CreatedBy, j.NotBefore, j.Deadline).
		Scan(&j.ID, &j.CreatedAt)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO job_runs(job_id, device_id, status) SELECT $1, unnest($2::text[]), $3`,
		j.ID, deviceIDs, RunQueued); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListJobs(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.pool.Query(ctx, `SELECT `+jobCols+` FROM jobs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	var out []Job
	idx := map[int64]int{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		j.Counts = map[string]int{}
		idx[j.ID] = len(out)
		out = append(out, *j)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	ids := make([]int64, 0, len(out))
	for _, j := range out {
		ids = append(ids, j.ID)
	}
	crows, err := p.pool.Query(ctx,
		`SELECT job_id, status, count(*) FROM job_runs WHERE job_id = ANY($1) GROUP BY job_id, status`, ids)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var id int64
		var st string
		var n int
		if err := crows.Scan(&id, &st, &n); err != nil {
			return nil, err
		}
		out[idx[id]].Counts[st] = n
	}
	return out, crows.Err()
}

func (p *Postgres) GetJob(ctx context.Context, id int64) (*Job, []JobRun, error) {
	j, err := scanJob(p.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM jobs WHERE id=$1`, id))
	if err != nil {
		return nil, nil, err
	}
	rows, err := p.pool.Query(ctx,
		`SELECT job_id, device_id, status, exit_code, output, sent_at, finished_at FROM job_runs WHERE job_id=$1 ORDER BY device_id`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var runs []JobRun
	for rows.Next() {
		var r JobRun
		if err := rows.Scan(&r.JobID, &r.DeviceID, &r.Status, &r.ExitCode, &r.Output, &r.SentAt, &r.FinishedAt); err != nil {
			return nil, nil, err
		}
		runs = append(runs, r)
	}
	return j, runs, rows.Err()
}

func (p *Postgres) CancelJob(ctx context.Context, id int64) error {
	var exists bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	_, err := p.pool.Exec(ctx,
		`UPDATE job_runs SET status=$2, finished_at=now() WHERE job_id=$1 AND status=$3`, id, RunCancelled, RunQueued)
	return err
}

func (p *Postgres) ClaimRuns(ctx context.Context, deviceID string, now time.Time) ([]Job, error) {
	rows, err := p.pool.Query(ctx,
		`UPDATE job_runs r SET status=$2, sent_at=$3
		 FROM jobs j
		 WHERE r.job_id=j.id AND r.device_id=$1 AND r.status=$4 AND j.not_before <= $3 AND j.deadline > $3
		 RETURNING j.id, j.kind, j.name, j.shell, j.body, j.run_as, j.timeout_sec, j.action, j.target,
		           j.created_by, j.not_before, j.deadline, j.created_at`,
		deviceID, RunSent, now, RunQueued)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// deterministic order
	for i := 1; i < len(out); i++ {
		for k := i; k > 0 && out[k].ID < out[k-1].ID; k-- {
			out[k], out[k-1] = out[k-1], out[k]
		}
	}
	return out, nil
}

func (p *Postgres) FinishRun(ctx context.Context, jobID int64, deviceID, status string, exitCode int, output string, now time.Time) error {
	err := affected(p.pool.Exec(ctx,
		`UPDATE job_runs SET status=$3, exit_code=$4, output=$5, finished_at=$6
		 WHERE job_id=$1 AND device_id=$2 AND status=$7`,
		jobID, deviceID, status, exitCode, output, now, RunSent))
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return err
}

func (p *Postgres) ExpireRuns(ctx context.Context, now time.Time) error {
	_, err := p.pool.Exec(ctx,
		`UPDATE job_runs r SET status=$1, finished_at=$2
		 FROM jobs j
		 WHERE r.job_id=j.id AND (
		   (r.status=$3 AND j.deadline <= $2) OR
		   (r.status=$4 AND j.deadline + make_interval(secs => j.timeout_sec) + interval '5 minutes' <= $2))`,
		RunExpired, now, RunQueued, RunSent)
	return err
}
