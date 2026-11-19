package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func (p *Postgres) SetDeviceIP(ctx context.Context, id, ip string) error {
	return affected(p.pool.Exec(ctx, `UPDATE devices SET ip=$2 WHERE id=$1`, id, ip))
}

func (p *Postgres) SetDeviceSync(ctx context.Context, id, report string, at time.Time) error {
	return affected(p.pool.Exec(ctx, `UPDATE devices SET sync=$2, sync_at=$3 WHERE id=$1`, id, report, at))
}

func (p *Postgres) SetDeviceInventory(ctx context.Context, id, inventory, disks string, at time.Time) error {
	return affected(p.pool.Exec(ctx, `UPDATE devices SET inventory=$2, inventory_at=$3, disks=$4 WHERE id=$1`, id, inventory, at, disks))
}

func (p *Postgres) GetDeviceInventory(ctx context.Context, id string) (string, error) {
	var inv string
	err := p.pool.QueryRow(ctx, `SELECT inventory FROM devices WHERE id=$1`, id).Scan(&inv)
	return inv, notFound(err)
}

func (p *Postgres) AddRelease(ctx context.Context, r *Release) error {
	err := p.pool.QueryRow(ctx,
		`INSERT INTO releases(build, version, sha256, size) VALUES($1,$2,$3,$4) RETURNING created_at`,
		r.Build, r.Version, r.SHA256, r.Size).Scan(&r.CreatedAt)
	if pe, ok := err.(*pgconn.PgError); ok && pe.Code == "23505" {
		return ErrConflict
	}
	return err
}

func (p *Postgres) ListReleases(ctx context.Context) ([]Release, error) {
	rows, err := p.pool.Query(ctx, `SELECT build, version, sha256, size, current, created_at FROM releases ORDER BY build DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Release
	for rows.Next() {
		var r Release
		if err := rows.Scan(&r.Build, &r.Version, &r.SHA256, &r.Size, &r.Current, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) SetCurrentRelease(ctx context.Context, build int) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE releases SET current=false WHERE current`); err != nil {
		return err
	}
	if build != 0 {
		if err := affected(tx.Exec(ctx, `UPDATE releases SET current=true WHERE build=$1`, build)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) DeleteRelease(ctx context.Context, build int) error {
	return affected(p.pool.Exec(ctx, `DELETE FROM releases WHERE build=$1`, build))
}

func (p *Postgres) HasRecentJob(ctx context.Context, deviceID, name string, since time.Time) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM jobs j JOIN job_runs r ON r.job_id=j.id
		  WHERE r.device_id=$1 AND j.name=$2 AND j.created_at >= $3)`,
		deviceID, name, since).Scan(&ok)
	return ok, err
}
