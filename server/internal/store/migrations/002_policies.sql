-- Settings pushed to devices. scope_id 0 = whole account, otherwise a device group id.
CREATE TABLE policies (
    scope_id    BIGINT PRIMARY KEY,
    "values"    JSONB NOT NULL DEFAULT '{}',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
