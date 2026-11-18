CREATE TABLE users (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    password_hash   TEXT NOT NULL,
    is_admin        BOOLEAN NOT NULL DEFAULT FALSE,
    totp_secret     TEXT NOT NULL DEFAULT '',
    totp_enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    totp_last_step  BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_name_idx ON users (lower(name));

CREATE TABLE sessions (
    token_hash  TEXT PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    client_id   TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL,
    last_seen   TIMESTAMPTZ NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

-- Locations, optionally nested ("Office" -> "Rack 1").
CREATE TABLE device_groups (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    parent_id  BIGINT REFERENCES device_groups(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX device_groups_name_idx
    ON device_groups (COALESCE(parent_id, 0), lower(name));

CREATE TABLE devices (
    id           TEXT PRIMARY KEY,
    uuid         TEXT NOT NULL,
    group_id     BIGINT REFERENCES device_groups(id) ON DELETE RESTRICT,
    hostname     TEXT NOT NULL DEFAULT '',
    username     TEXT NOT NULL DEFAULT '',
    os           TEXT NOT NULL DEFAULT '',
    cpu          TEXT NOT NULL DEFAULT '',
    memory       TEXT NOT NULL DEFAULT '',
    version      TEXT NOT NULL DEFAULT '',
    alias        TEXT NOT NULL DEFAULT '',
    note         TEXT NOT NULL DEFAULT '',
    password_h1  TEXT NOT NULL DEFAULT '',
    conns        INT  NOT NULL DEFAULT 0,
    last_seen    TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX devices_group_idx ON devices (group_id);

CREATE TABLE enroll_tokens (
    id          BIGSERIAL PRIMARY KEY,
    token_hash  TEXT NOT NULL UNIQUE,
    group_id    BIGINT REFERENCES device_groups(id) ON DELETE RESTRICT,
    note        TEXT NOT NULL DEFAULT '',
    max_uses    INT NOT NULL DEFAULT 0,
    uses        INT NOT NULL DEFAULT 0,
    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
