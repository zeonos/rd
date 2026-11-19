package store

import (
	"context"
	"time"
)

// Release is an uploaded Windows client build that devices can update to.
type Release struct {
	Build     int       `json:"build"`
	Version   string    `json:"version"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	Current   bool      `json:"current"`
	CreatedAt time.Time `json:"created_at"`
}

// FleetStore is part of Store: device IP and inventory, releases.
type FleetStore interface {
	SetDeviceIP(ctx context.Context, id, ip string) error
	SetDeviceInventory(ctx context.Context, id, inventory string, at time.Time) error
	GetDeviceInventory(ctx context.Context, id string) (string, error)

	AddRelease(ctx context.Context, r *Release) error // ErrConflict if the build exists
	ListReleases(ctx context.Context) ([]Release, error)
	// SetCurrentRelease makes build the one devices update to; 0 = none.
	SetCurrentRelease(ctx context.Context, build int) error
	DeleteRelease(ctx context.Context, build int) error

	// HasRecentJob reports whether a job with this name was created for the
	// device since the given time.
	HasRecentJob(ctx context.Context, deviceID, name string, since time.Time) (bool, error)
}
