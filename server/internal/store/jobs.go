package store

import (
	"context"
	"time"
)

type Script struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Shell       string `json:"shell"` // powershell | cmd
	Body        string `json:"body"`
	RunAs       string `json:"run_as"` // system | user
	TimeoutSec  int    `json:"timeout_sec"`
	// Params are the variables the script uses. Their values come from the
	// device, its location or the default here, resolved when a job is created.
	Params    []ScriptParam `json:"params"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type ScriptParam struct {
	Name        string `json:"name"`
	Default     string `json:"default"`
	Description string `json:"description"`
}

type Job struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"` // script | power
	Name       string    `json:"name"`
	Shell      string    `json:"shell"`
	Body       string    `json:"body"`
	RunAs      string    `json:"run_as"`
	TimeoutSec int       `json:"timeout_sec"`
	Action     string    `json:"action"`
	Target     string    `json:"target"`
	CreatedBy  string    `json:"created_by"`
	NotBefore  time.Time `json:"not_before"`
	Deadline   time.Time `json:"deadline"`
	CreatedAt  time.Time `json:"created_at"`
	ScriptID   *int64    `json:"script_id"`   // saved script it came from, if any
	ScheduleID *int64    `json:"schedule_id"` // schedule that created it, if any
	// Counts by run status, filled by ListJobs.
	Counts map[string]int `json:"counts,omitempty"`
	// RunVars are the resolved variables per device, stored by CreateJob.
	RunVars map[string]map[string]string `json:"-"`
	// Vars are this device's variables, filled by ClaimRuns.
	Vars map[string]string `json:"-"`
}

type JobFilter struct {
	ScriptID   int64
	ScheduleID int64
	Limit      int
}

const (
	RunQueued    = "queued"
	RunSent      = "sent"
	RunSuccess   = "success"
	RunFailed    = "failed"
	RunExpired   = "expired"
	RunCancelled = "cancelled"
)

func RunFinal(status string) bool {
	switch status {
	case RunSuccess, RunFailed, RunExpired, RunCancelled:
		return true
	}
	return false
}

type JobRun struct {
	JobID      int64             `json:"job_id"`
	DeviceID   string            `json:"device_id"`
	Status     string            `json:"status"`
	ExitCode   *int              `json:"exit_code"`
	Output     string            `json:"output"`
	SentAt     *time.Time        `json:"sent_at"`
	FinishedAt *time.Time        `json:"finished_at"`
	Vars       map[string]string `json:"vars,omitempty"`
}

// JobStore is part of Store.
type JobStore interface {
	SetDevicePublicKey(ctx context.Context, id, key string) error
	GetServerKey(ctx context.Context, name string) (string, error)
	// PutServerKeyIfAbsent stores value unless the name exists; returns the stored value.
	PutServerKeyIfAbsent(ctx context.Context, name, value string) (string, error)

	ListScripts(ctx context.Context) ([]Script, error)
	GetScript(ctx context.Context, id int64) (*Script, error)
	SaveScript(ctx context.Context, s *Script) error // insert when ID == 0
	DeleteScript(ctx context.Context, id int64) error

	// CreateJob inserts the job and one queued run per device, with
	// j.RunVars[device] as that run's variables.
	CreateJob(ctx context.Context, j *Job, deviceIDs []string) error
	ListJobs(ctx context.Context, f JobFilter) ([]Job, error)
	GetJob(ctx context.Context, id int64) (*Job, []JobRun, error)
	CancelJob(ctx context.Context, id int64) error
	// ClaimRuns marks due queued runs of a device as sent and returns them.
	ClaimRuns(ctx context.Context, deviceID string, now time.Time) ([]Job, error)
	// FinishRun records a result for a sent run; ErrNotFound if not pending.
	FinishRun(ctx context.Context, jobID int64, deviceID, status string, exitCode int, output string, now time.Time) error
	// ExpireRuns marks queued/sent runs past their deadline as expired.
	ExpireRuns(ctx context.Context, now time.Time) error
}
