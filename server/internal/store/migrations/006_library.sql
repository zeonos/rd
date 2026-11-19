-- Script library: variables, schedules and run history.

-- Variables a script uses: [{"name","default","description"}].
ALTER TABLE scripts ADD COLUMN params JSONB NOT NULL DEFAULT '[]';

-- Values per device and per location (inherited by sublocations).
ALTER TABLE devices ADD COLUMN vars JSONB NOT NULL DEFAULT '{}';
ALTER TABLE device_groups ADD COLUMN vars JSONB NOT NULL DEFAULT '{}';

-- Where a job came from, and the values each run got.
ALTER TABLE jobs ADD COLUMN script_id   BIGINT;
ALTER TABLE jobs ADD COLUMN schedule_id BIGINT;
ALTER TABLE job_runs ADD COLUMN vars JSONB NOT NULL DEFAULT '{}';
CREATE INDEX jobs_script_idx   ON jobs (script_id, id DESC) WHERE script_id IS NOT NULL;
CREATE INDEX jobs_schedule_idx ON jobs (schedule_id, id DESC) WHERE schedule_id IS NOT NULL;
CREATE INDEX job_runs_device_idx ON job_runs (device_id, job_id DESC);

CREATE TABLE schedules (
    id             BIGSERIAL PRIMARY KEY,
    script_id      BIGINT NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,             -- hours | days
    every_hours    INT  NOT NULL DEFAULT 0,
    at_minute      INT  NOT NULL DEFAULT 0,   -- minutes after midnight in tz
    weekdays       INT  NOT NULL DEFAULT 127, -- bit 0 = Sunday
    tz             TEXT NOT NULL DEFAULT 'UTC',
    target         JSONB NOT NULL,
    target_text    TEXT NOT NULL DEFAULT '',
    vars           JSONB NOT NULL DEFAULT '{}',
    deadline_hours INT  NOT NULL DEFAULT 1,
    enabled        BOOLEAN NOT NULL DEFAULT true,
    next_run       TIMESTAMPTZ NOT NULL,
    last_run       TIMESTAMPTZ,
    last_job_id    BIGINT,
    created_by     TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX schedules_due_idx ON schedules (next_run) WHERE enabled;
