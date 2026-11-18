-- Device public key (Ed25519, base64), used to verify agent requests.
ALTER TABLE devices ADD COLUMN public_key TEXT NOT NULL DEFAULT '';

-- Server-side secrets (e.g. the job signing key).
CREATE TABLE server_keys (
    name        TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE scripts (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    shell        TEXT NOT NULL,             -- powershell | cmd
    body         TEXT NOT NULL,
    run_as       TEXT NOT NULL DEFAULT 'system', -- system | user
    timeout_sec  INT  NOT NULL DEFAULT 300,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX scripts_name_idx ON scripts (lower(name));

-- A job is a snapshot of what to run, so editing a script later does not
-- change jobs already sent.
CREATE TABLE jobs (
    id           BIGSERIAL PRIMARY KEY,
    kind         TEXT NOT NULL,             -- script | power
    name         TEXT NOT NULL,
    shell        TEXT NOT NULL DEFAULT '',
    body         TEXT NOT NULL DEFAULT '',
    run_as       TEXT NOT NULL DEFAULT 'system',
    timeout_sec  INT  NOT NULL DEFAULT 300,
    action       TEXT NOT NULL DEFAULT '',  -- reboot | reboot-safe-mode | shutdown | logoff
    target       TEXT NOT NULL DEFAULT '',  -- human description of the selection
    created_by   TEXT NOT NULL,
    not_before   TIMESTAMPTZ NOT NULL,
    deadline     TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE job_runs (
    job_id       BIGINT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    device_id    TEXT NOT NULL,
    status       TEXT NOT NULL,             -- queued | sent | success | failed | expired | cancelled
    exit_code    INT,
    output       TEXT NOT NULL DEFAULT '',
    sent_at      TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,
    PRIMARY KEY (job_id, device_id)
);
CREATE INDEX job_runs_device_status_idx ON job_runs (device_id, status);
