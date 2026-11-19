package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Postgres struct {
	pool *pgxpool.Pool
}

// OpenPostgres connects, waits for the database and applies migrations.
func OpenPostgres(ctx context.Context, url string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 8
	var pool *pgxpool.Pool
	for attempt := 0; ; attempt++ {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				break
			}
			pool.Close()
		}
		if attempt >= 30 {
			return nil, fmt.Errorf("connect to database: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	p := &Postgres{pool: pool}
	if err := p.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return p, nil
}

func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) migrate(ctx context.Context) error {
	if _, err := p.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		var exists bool
		if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sql, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := p.pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(name) VALUES($1)`, name); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func isFKViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23503"
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func affected(tag pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- users ---

const userCols = `id, name, password_hash, is_admin, totp_secret, totp_enabled, totp_last_step, created_at`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Name, &u.PasswordHash, &u.IsAdmin, &u.TOTPSecret, &u.TOTPEnabled, &u.TOTPLastStep, &u.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

func (p *Postgres) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := p.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (p *Postgres) CreateUser(ctx context.Context, u *User) error {
	err := p.pool.QueryRow(ctx,
		`INSERT INTO users(name, password_hash, is_admin, totp_secret, totp_enabled)
		 VALUES($1,$2,$3,$4,$5) RETURNING id, created_at`,
		u.Name, u.PasswordHash, u.IsAdmin, u.TOTPSecret, u.TOTPEnabled).Scan(&u.ID, &u.CreatedAt)
	if isUnique(err) {
		return ErrConflict
	}
	return err
}

func (p *Postgres) GetUserByName(ctx context.Context, name string) (*User, error) {
	return scanUser(p.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(name)=lower($1)`, name))
}

func (p *Postgres) GetUserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(p.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

func (p *Postgres) SetUserPassword(ctx context.Context, id int64, hash string) error {
	return affected(p.pool.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, id, hash))
}

func (p *Postgres) SetUserTOTP(ctx context.Context, id int64, secret string, enabled bool) error {
	return affected(p.pool.Exec(ctx,
		`UPDATE users SET totp_secret=$2, totp_enabled=$3, totp_last_step=0 WHERE id=$1`, id, secret, enabled))
}

func (p *Postgres) AdvanceTOTPStep(ctx context.Context, id int64, step int64) (bool, error) {
	tag, err := p.pool.Exec(ctx, `UPDATE users SET totp_last_step=$2 WHERE id=$1 AND totp_last_step < $2`, id, step)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// --- sessions ---

func (p *Postgres) CreateSession(ctx context.Context, s *Session) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO sessions(token_hash, user_id, kind, client_id, created_at, last_seen, expires_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7)`,
		s.TokenHash, s.UserID, s.Kind, s.ClientID, s.CreatedAt, s.LastSeen, s.ExpiresAt)
	return err
}

func (p *Postgres) GetSession(ctx context.Context, h string) (*Session, error) {
	var s Session
	err := p.pool.QueryRow(ctx,
		`SELECT token_hash, user_id, kind, client_id, created_at, last_seen, expires_at FROM sessions WHERE token_hash=$1`, h).
		Scan(&s.TokenHash, &s.UserID, &s.Kind, &s.ClientID, &s.CreatedAt, &s.LastSeen, &s.ExpiresAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &s, nil
}

func (p *Postgres) TouchSession(ctx context.Context, h string, at time.Time) error {
	_, err := p.pool.Exec(ctx, `UPDATE sessions SET last_seen=$2 WHERE token_hash=$1`, h, at)
	return err
}

func (p *Postgres) DeleteSession(ctx context.Context, h string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, h)
	return err
}

func (p *Postgres) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
	return err
}

func (p *Postgres) DeleteExpiredSessions(ctx context.Context, now time.Time, idle time.Duration) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= $1 OR last_seen < $2`, now, now.Add(-idle))
	return err
}

// --- groups ---

func (p *Postgres) loadGroups(ctx context.Context) (map[int64]*Group, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, name, parent_id FROM device_groups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int64]*Group{}
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.ParentID); err != nil {
			return nil, err
		}
		m[g.ID] = &g
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, g := range m {
		parts := []string{g.Name}
		seen := map[int64]bool{g.ID: true}
		for pid := g.ParentID; pid != nil && !seen[*pid]; {
			pg, ok := m[*pid]
			if !ok {
				break
			}
			seen[pg.ID] = true
			parts = append([]string{pg.Name}, parts...)
			pid = pg.ParentID
		}
		g.Path = JoinPath(parts)
	}
	return m, nil
}

func (p *Postgres) ListGroups(ctx context.Context) ([]Group, error) {
	m, err := p.loadGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(m))
	for _, g := range m {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out, nil
}

func (p *Postgres) GetGroup(ctx context.Context, id int64) (*Group, error) {
	m, err := p.loadGroups(ctx)
	if err != nil {
		return nil, err
	}
	g, ok := m[id]
	if !ok {
		return nil, ErrNotFound
	}
	return g, nil
}

func (p *Postgres) findChild(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, parent *int64, name string) (int64, error) {
	var id int64
	err := q.QueryRow(ctx,
		`SELECT id FROM device_groups WHERE COALESCE(parent_id,0)=COALESCE($1,0) AND lower(name)=lower($2)`,
		parent, name).Scan(&id)
	return id, notFound(err)
}

func (p *Postgres) EnsureGroupPath(ctx context.Context, path string) (*Group, error) {
	parts := SplitPath(path)
	if len(parts) == 0 {
		return nil, ErrNotFound
	}
	var parent *int64
	for _, name := range parts {
		id, err := p.findChild(ctx, p.pool, parent, name)
		if errors.Is(err, ErrNotFound) {
			err = p.pool.QueryRow(ctx,
				`INSERT INTO device_groups(name, parent_id) VALUES($1,$2)
				 ON CONFLICT DO NOTHING RETURNING id`, name, parent).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) { // created concurrently
				id, err = p.findChild(ctx, p.pool, parent, name)
			}
		}
		if err != nil {
			return nil, err
		}
		pid := id
		parent = &pid
	}
	return p.GetGroup(ctx, *parent)
}

func (p *Postgres) FindGroupPath(ctx context.Context, path string) (*Group, error) {
	parts := SplitPath(path)
	if len(parts) == 0 {
		return nil, ErrNotFound
	}
	var parent *int64
	for _, name := range parts {
		id, err := p.findChild(ctx, p.pool, parent, name)
		if err != nil {
			return nil, err
		}
		pid := id
		parent = &pid
	}
	return p.GetGroup(ctx, *parent)
}

func (p *Postgres) RenameGroup(ctx context.Context, id int64, name string) error {
	err := affected(p.pool.Exec(ctx, `UPDATE device_groups SET name=$2 WHERE id=$1`, id, name))
	if isUnique(err) {
		return ErrConflict
	}
	return err
}

func (p *Postgres) DeleteGroup(ctx context.Context, id int64) error {
	err := affected(p.pool.Exec(ctx, `DELETE FROM device_groups WHERE id=$1`, id))
	if isFKViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `DELETE FROM policies WHERE scope_id=$1`, id)
	return err
}

// --- policies ---

func (p *Postgres) ListPolicies(ctx context.Context) (map[int64]map[string]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT scope_id, "values" FROM policies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]string{}
	for rows.Next() {
		var id int64
		var v map[string]string
		if err := rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}

func (p *Postgres) SetPolicy(ctx context.Context, scope int64, values map[string]string) error {
	if scope != AccountScope {
		var exists bool
		if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM device_groups WHERE id=$1)`, scope).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	if len(values) == 0 {
		_, err := p.pool.Exec(ctx, `DELETE FROM policies WHERE scope_id=$1`, scope)
		return err
	}
	_, err := p.pool.Exec(ctx,
		`INSERT INTO policies(scope_id, "values", updated_at) VALUES($1,$2,now())
		 ON CONFLICT (scope_id) DO UPDATE SET "values"=EXCLUDED."values", updated_at=now()`, scope, values)
	return err
}

// --- devices ---

const deviceCols = `id, uuid, group_id, hostname, username, os, cpu, memory, version, alias, note,
	password_h1, public_key, conns, last_seen, created_at, updated_at, ip, build, inventory_at`

func scanDevice(row pgx.Row) (*Device, error) {
	var d Device
	err := row.Scan(&d.ID, &d.UUID, &d.GroupID, &d.Hostname, &d.Username, &d.OS, &d.CPU, &d.Memory,
		&d.Version, &d.Alias, &d.Note, &d.PasswordH1, &d.PublicKey, &d.Conns, &d.LastSeen, &d.CreatedAt, &d.UpdatedAt,
		&d.IP, &d.Build, &d.InventoryAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &d, nil
}

func (p *Postgres) ListDevices(ctx context.Context) ([]Device, error) {
	groups, err := p.loadGroups(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `SELECT `+deviceCols+` FROM devices ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		if d.GroupID != nil {
			if g, ok := groups[*d.GroupID]; ok {
				d.GroupPath = g.Path
			}
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (p *Postgres) GetDevice(ctx context.Context, id string) (*Device, error) {
	d, err := scanDevice(p.pool.QueryRow(ctx, `SELECT `+deviceCols+` FROM devices WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	if d.GroupID != nil {
		if g, err := p.GetGroup(ctx, *d.GroupID); err == nil {
			d.GroupPath = g.Path
		}
	}
	return d, nil
}

func (p *Postgres) EnrollDevice(ctx context.Context, d *Device) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO devices(id, uuid, group_id, alias, note) VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (id) DO UPDATE SET
		   password_h1 = CASE WHEN devices.uuid = EXCLUDED.uuid THEN devices.password_h1 ELSE '' END,
		   public_key = CASE WHEN devices.uuid = EXCLUDED.uuid THEN devices.public_key ELSE '' END,
		   uuid = EXCLUDED.uuid,
		   group_id = EXCLUDED.group_id,
		   alias = CASE WHEN EXCLUDED.alias <> '' THEN EXCLUDED.alias ELSE devices.alias END,
		   note = CASE WHEN EXCLUDED.note <> '' THEN EXCLUDED.note ELSE devices.note END,
		   updated_at = now()`,
		d.ID, d.UUID, d.GroupID, d.Alias, d.Note)
	return err
}

func (p *Postgres) UpdateSysinfo(ctx context.Context, id string, u SysinfoUpdate) error {
	return affected(p.pool.Exec(ctx,
		`UPDATE devices SET hostname=$2, username=$3, os=$4, cpu=$5, memory=$6, version=$7,
		   password_h1 = CASE WHEN $8 <> '' THEN $8 ELSE password_h1 END,
		   build = CASE WHEN $9 > 0 THEN $9 ELSE build END, updated_at=now()
		 WHERE id=$1`,
		id, u.Hostname, u.Username, u.OS, u.CPU, u.Memory, u.Version, u.PasswordH1, u.Build))
}

func (p *Postgres) Heartbeat(ctx context.Context, id string, conns int, at time.Time) error {
	return affected(p.pool.Exec(ctx, `UPDATE devices SET last_seen=$2, conns=$3 WHERE id=$1`, id, at, conns))
}

func (p *Postgres) UpdateDevice(ctx context.Context, id string, alias, note *string, groupID **int64) error {
	sets := []string{"updated_at=now()"}
	args := []any{id}
	if alias != nil {
		args = append(args, *alias)
		sets = append(sets, fmt.Sprintf("alias=$%d", len(args)))
	}
	if note != nil {
		args = append(args, *note)
		sets = append(sets, fmt.Sprintf("note=$%d", len(args)))
	}
	if groupID != nil {
		args = append(args, *groupID)
		sets = append(sets, fmt.Sprintf("group_id=$%d", len(args)))
	}
	err := affected(p.pool.Exec(ctx, `UPDATE devices SET `+strings.Join(sets, ", ")+` WHERE id=$1`, args...))
	if isFKViolation(err) {
		return ErrNotFound
	}
	return err
}

func (p *Postgres) DeleteDevice(ctx context.Context, id string) error {
	return affected(p.pool.Exec(ctx, `DELETE FROM devices WHERE id=$1`, id))
}

// --- enrolment tokens ---

const tokenCols = `id, token_hash, group_id, note, max_uses, uses, expires_at, created_at`

func scanToken(row pgx.Row) (*EnrollToken, error) {
	var t EnrollToken
	err := row.Scan(&t.ID, &t.TokenHash, &t.GroupID, &t.Note, &t.MaxUses, &t.Uses, &t.ExpiresAt, &t.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &t, nil
}

func (p *Postgres) CreateEnrollToken(ctx context.Context, t *EnrollToken) error {
	return p.pool.QueryRow(ctx,
		`INSERT INTO enroll_tokens(token_hash, group_id, note, max_uses, expires_at)
		 VALUES($1,$2,$3,$4,$5) RETURNING id, created_at`,
		t.TokenHash, t.GroupID, t.Note, t.MaxUses, t.ExpiresAt).Scan(&t.ID, &t.CreatedAt)
}

func (p *Postgres) ListEnrollTokens(ctx context.Context) ([]EnrollToken, error) {
	groups, err := p.loadGroups(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `SELECT `+tokenCols+` FROM enroll_tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EnrollToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		if t.GroupID != nil {
			if g, ok := groups[*t.GroupID]; ok {
				t.GroupPath = g.Path
			}
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (p *Postgres) UseEnrollToken(ctx context.Context, h string, now time.Time) (*EnrollToken, error) {
	t, err := scanToken(p.pool.QueryRow(ctx,
		`UPDATE enroll_tokens SET uses = uses + 1
		 WHERE token_hash=$1 AND (expires_at IS NULL OR expires_at > $2) AND (max_uses = 0 OR uses < max_uses)
		 RETURNING `+tokenCols, h, now))
	if errors.Is(err, ErrNotFound) {
		var exists bool
		if e := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM enroll_tokens WHERE token_hash=$1)`, h).Scan(&exists); e != nil {
			return nil, e
		}
		if exists {
			return nil, ErrTokenUnusable
		}
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if t.GroupID != nil {
		if g, err := p.GetGroup(ctx, *t.GroupID); err == nil {
			t.GroupPath = g.Path
		}
	}
	return t, nil
}

func (p *Postgres) DeleteEnrollToken(ctx context.Context, id int64) error {
	return affected(p.pool.Exec(ctx, `DELETE FROM enroll_tokens WHERE id=$1`, id))
}
