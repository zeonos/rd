# RD API server

Go service that the RustDesk-based client talks to as its "API server":

| Endpoint | Used by | Purpose |
| --- | --- | --- |
| `POST /api/login` | viewer | password, then TOTP code (the client's 2FA dialog) |
| `POST /api/currentUser`, `/api/logout` | viewer | session |
| `GET /api/ab` | viewer | address book: all devices, tagged by location, with the unattended-access hash |
| `GET /api/peers`, `/api/device-group/accessible`, `/api/users` | viewer | "Accessible devices" tab |
| `POST /api/devices/cli` | `rustdesk.exe --assign` | enrolment with a token |
| `POST /api/heartbeat`, `/api/sysinfo` | agent | presence and device info |
| `/api/admin/*` | web console (phase 2), scripts | devices, groups, tokens (admin bearer token) |

Admin CLI: `rd-api help`.

## Development

```bash
go test ./...                                   # in-memory store
RD_TEST_DATABASE_URL=postgres://... go test ./... # also against PostgreSQL
```

`go.mod` maps `golang.org/x/*` and `gopkg.in/*` modules to their official
GitHub mirrors and builds use `GOPROXY=direct`, so dependencies come straight
from GitHub (the Dockerfile and CI do this).
