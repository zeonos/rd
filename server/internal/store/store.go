// Package store defines the persistent data model of the RD API server and
// two implementations: PostgreSQL (production) and in-memory (tests).
package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
	// ErrTokenUnusable is returned when an enrolment token is expired or used up.
	ErrTokenUnusable = errors.New("enrolment token expired or used up")
)

type User struct {
	ID           int64
	Name         string
	PasswordHash string
	IsAdmin      bool
	TOTPSecret   string // base32, empty until set up
	TOTPEnabled  bool
	TOTPLastStep int64 // last accepted TOTP time step, blocks code reuse
	CreatedAt    time.Time
}

type Session struct {
	TokenHash string
	UserID    int64
	Kind      string // "client" (RustDesk app) or "web"
	ClientID  string // RustDesk ID of the viewer that logged in, if any
	CreatedAt time.Time
	LastSeen  time.Time
	ExpiresAt time.Time // absolute expiry
}

// Group is a location (optionally nested, e.g. "Office/Rack 1").
type Group struct {
	ID       int64
	Name     string
	ParentID *int64
	Path     string // full path joined with "/", filled by List/Get
}

type Device struct {
	ID        string // RustDesk ID
	UUID      string // base64 machine UUID reported by the client
	GroupID   *int64
	GroupPath string // filled on read
	Hostname  string
	Username  string // logged-on OS user
	OS        string
	CPU       string
	Memory    string
	Version   string
	Alias     string
	Note      string
	// PasswordH1 is base64(sha256(permanent password + device salt)), reported by
	// the patched client. The address book hands it to logged-in viewers so they
	// can connect unattended without anyone knowing the device password.
	PasswordH1 string
	// PublicKey is the device's Ed25519 key (base64), recorded on its first
	// sysinfo after enrolment; agent requests must be signed with it.
	PublicKey string
	Conns     int
	LastSeen  *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time

	IP          string     // public address of the last heartbeat
	Build       int        // RD build number the client reports; 0 = unknown
	InventoryAt *time.Time // when the agent last sent its inventory
	Disks       string     // JSON array from the last inventory: [{name,label,size,free}]
}

// Online reports whether the device sent a heartbeat recently.
func (d *Device) Online(now time.Time) bool {
	return d.LastSeen != nil && now.Sub(*d.LastSeen) < 60*time.Second
}

type EnrollToken struct {
	ID        int64
	TokenHash string
	GroupID   *int64 // nil: device chooses its group via --device_group_name
	GroupPath string
	Note      string
	MaxUses   int // 0 = unlimited
	Uses      int
	ExpiresAt *time.Time
	CreatedAt time.Time
}

func (t *EnrollToken) Usable(now time.Time) bool {
	if t.ExpiresAt != nil && !now.Before(*t.ExpiresAt) {
		return false
	}
	return t.MaxUses == 0 || t.Uses < t.MaxUses
}

// SysinfoUpdate is what a device reports about itself.
type SysinfoUpdate struct {
	Hostname   string
	Username   string
	OS         string
	CPU        string
	Memory     string
	Version    string
	PasswordH1 string // empty = leave unchanged
	Build      int    // RD build number; 0 = leave unchanged
}

type Store interface {
	JobStore
	FleetStore
	Close()

	// Users
	CountUsers(ctx context.Context) (int, error)
	CreateUser(ctx context.Context, u *User) error
	GetUserByName(ctx context.Context, name string) (*User, error)
	GetUserByID(ctx context.Context, id int64) (*User, error)
	SetUserPassword(ctx context.Context, id int64, hash string) error
	SetUserTOTP(ctx context.Context, id int64, secret string, enabled bool) error
	// AdvanceTOTPStep stores step if it is newer than the stored one and reports
	// whether it was (false = code replay).
	AdvanceTOTPStep(ctx context.Context, id int64, step int64) (bool, error)

	// Sessions
	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, tokenHash string) (*Session, error)
	TouchSession(ctx context.Context, tokenHash string, at time.Time) error
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteUserSessions(ctx context.Context, userID int64) error
	DeleteExpiredSessions(ctx context.Context, now time.Time, idle time.Duration) error

	// Groups
	ListGroups(ctx context.Context) ([]Group, error)
	GetGroup(ctx context.Context, id int64) (*Group, error)
	// EnsureGroupPath finds or creates every level of "A/B/C" and returns the leaf.
	EnsureGroupPath(ctx context.Context, path string) (*Group, error)
	FindGroupPath(ctx context.Context, path string) (*Group, error)
	RenameGroup(ctx context.Context, id int64, name string) error
	// DeleteGroup fails with ErrConflict if the group has devices or children.
	DeleteGroup(ctx context.Context, id int64) error

	// Devices
	ListDevices(ctx context.Context) ([]Device, error)
	GetDevice(ctx context.Context, id string) (*Device, error)
	// EnrollDevice creates the device or re-binds it (new UUID, group) on reinstall.
	EnrollDevice(ctx context.Context, d *Device) error
	UpdateSysinfo(ctx context.Context, id string, u SysinfoUpdate) error
	Heartbeat(ctx context.Context, id string, conns int, at time.Time) error
	UpdateDevice(ctx context.Context, id string, alias, note *string, groupID **int64) error
	DeleteDevice(ctx context.Context, id string) error

	// Policies: scope 0 = account, otherwise a group id. An empty map removes the scope's policy.
	ListPolicies(ctx context.Context) (map[int64]map[string]string, error)
	SetPolicy(ctx context.Context, scope int64, values map[string]string) error

	// Enrolment tokens
	CreateEnrollToken(ctx context.Context, t *EnrollToken) error
	ListEnrollTokens(ctx context.Context) ([]EnrollToken, error)
	// UseEnrollToken atomically checks and increments the use counter.
	UseEnrollToken(ctx context.Context, tokenHash string, now time.Time) (*EnrollToken, error)
	DeleteEnrollToken(ctx context.Context, id int64) error
}

// AccountScope is the policy scope of the whole account.
const AccountScope int64 = 0

// SplitPath normalises "A / B//C" to ["A","B","C"].
func SplitPath(path string) []string {
	var out []string
	for _, p := range strings.Split(path, "/") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// JoinPath is the inverse of SplitPath.
func JoinPath(parts []string) string { return strings.Join(parts, "/") }
