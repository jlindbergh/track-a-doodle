-- A connection's counts of imported, skipped and failed activities are now
-- counted from provider_activities, which records every activity the moment
-- it is handled. The counters on provider_connections were only saved once
-- per page and reset when a backfill was started again, so they lagged
-- behind and forgot activities already imported.

-- +goose Up
ALTER TABLE provider_connections
    DROP COLUMN backfill_imported,
    DROP COLUMN backfill_skipped,
    DROP COLUMN backfill_failed;

-- +goose Down
ALTER TABLE provider_connections
    ADD COLUMN backfill_imported integer NOT NULL DEFAULT 0,
    ADD COLUMN backfill_skipped  integer NOT NULL DEFAULT 0,
    ADD COLUMN backfill_failed   integer NOT NULL DEFAULT 0;

UPDATE provider_connections c SET
    backfill_imported = a.imported,
    backfill_skipped  = a.skipped,
    backfill_failed   = a.failed
  FROM (SELECT connection_id,
               count(*) FILTER (WHERE status = 'imported') AS imported,
               count(*) FILTER (WHERE status NOT IN ('imported', 'failed')) AS skipped,
               count(*) FILTER (WHERE status = 'failed') AS failed
          FROM provider_activities
         GROUP BY connection_id) a
 WHERE a.connection_id = c.id;
