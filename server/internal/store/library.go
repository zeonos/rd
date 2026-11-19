package store

import (
	"context"
	"time"
)

// Vars maps variable names to values.
type Vars = map[string]string

// JobTarget selects devices: the union of devices, locations (with
// sublocations) and all.
type JobTarget struct {
	Devices []string `json:"devices,omitempty"`
	Groups  []int64  `json:"groups,omitempty"`
	All     bool     `json:"all,omitempty"`
}

// Schedule runs a saved script again and again.
type Schedule struct {
	ID         int64  `json:"id"`
	ScriptID   int64  `json:"script_id"`
	ScriptName string `json:"script_name"` // filled on read
	// Kind "hours": every EveryHours hours. Kind "days": at AtMinute
	// (minutes after midnight in TZ) on the Weekdays set (bit 0 = Sunday).
	Kind          string     `json:"kind"`
	EveryHours    int        `json:"every_hours"`
	AtMinute      int        `json:"at_minute"`
	Weekdays      int        `json:"weekdays"`
	TZ            string     `json:"tz"`
	Target        JobTarget  `json:"target"`
	TargetText    string     `json:"target_text"`
	Vars          Vars       `json:"vars"` // overrides for every device
	DeadlineHours int        `json:"deadline_hours"`
	Enabled       bool       `json:"enabled"`
	NextRun       time.Time  `json:"next_run"`
	LastRun       *time.Time `json:"last_run"`
	LastJobID     *int64     `json:"last_job_id"`
	CreatedBy     string     `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
}

// DeviceRun is a run on one device with its job, for the device history.
type DeviceRun struct {
	JobRun
	JobName   string    `json:"job_name"`
	Kind      string    `json:"kind"`
	ScriptID  *int64    `json:"script_id"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

// LibraryStore is part of Store: variables, schedules and run history.
type LibraryStore interface {
	// AllVars returns the variables set on devices and on locations.
	AllVars(ctx context.Context) (devices map[string]Vars, groups map[int64]Vars, err error)
	SetDeviceVars(ctx context.Context, id string, v Vars) error
	SetGroupVars(ctx context.Context, id int64, v Vars) error

	ListDeviceRuns(ctx context.Context, deviceID string, limit int) ([]DeviceRun, error)

	ListSchedules(ctx context.Context) ([]Schedule, error)
	GetSchedule(ctx context.Context, id int64) (*Schedule, error)
	SaveSchedule(ctx context.Context, s *Schedule) error // insert when ID == 0
	DeleteSchedule(ctx context.Context, id int64) error
	// DueSchedules returns enabled schedules with NextRun <= now.
	DueSchedules(ctx context.Context, now time.Time) ([]Schedule, error)
	// MarkScheduleRun records a run (jobID nil if nothing was started) and the next run time.
	MarkScheduleRun(ctx context.Context, id int64, ranAt time.Time, jobID *int64, next time.Time) error
}
