-- Disk summary from the last inventory, for the device list.
ALTER TABLE devices ADD COLUMN disks TEXT NOT NULL DEFAULT '';
-- CASE keeps the cast away from rows without an inventory ('' is not JSON).
UPDATE devices SET disks = CASE
    WHEN inventory <> '' AND jsonb_typeof((CASE WHEN inventory <> '' THEN inventory::jsonb END) -> 'disks') = 'array'
    THEN ((CASE WHEN inventory <> '' THEN inventory::jsonb END) -> 'disks')::text
    ELSE '' END;
