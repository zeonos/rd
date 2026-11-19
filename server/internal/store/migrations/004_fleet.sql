-- Public IP as seen by the API server, RD build number, and inventory.
ALTER TABLE devices ADD COLUMN ip TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN build INT NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN inventory TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN inventory_at TIMESTAMPTZ;

-- Client releases for auto-update. The files live in RD_DATA_DIR/releases.
CREATE TABLE releases (
    build       INT PRIMARY KEY,
    version     TEXT NOT NULL DEFAULT '',
    sha256      TEXT NOT NULL,
    size        BIGINT NOT NULL,
    current     BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX releases_one_current ON releases (current) WHERE current;
