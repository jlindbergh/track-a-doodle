-- Connections to activity services (Garmin Connect for now), and what each
-- connection has synced.
--
-- SECURITY: credentials are encrypted by the backend (AES-256-GCM, key in
-- SECRETS_KEY, bound to the row's user and provider) and never stored or
-- logged in the clear. Every row belongs to one user and is deleted with
-- them.

-- +goose Up

-- The service's own id for an activity synced from it, e.g. Garmin's
-- activityId. NULL for uploaded files.
ALTER TABLE activities ADD COLUMN external_id text;
ALTER TABLE activities
    ADD CONSTRAINT activities_user_source_external_id_key UNIQUE (user_id, source, external_id);

CREATE TABLE provider_connections (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider   text        NOT NULL CHECK (provider IN ('garmin')),

    -- The user's personal API key, encrypted.
    api_key_enc     bytea       NOT NULL,
    -- When the key expires, if the user told us. Garmin doesn't say.
    key_expires_at  timestamptz,
    -- The short-lived access token made from the key, encrypted, so it
    -- survives restarts.
    access_token_enc        bytea,
    access_token_expires_at timestamptz,

    status     text        NOT NULL DEFAULT 'active'
               CHECK (status IN ('active', 'invalid_key', 'key_expired', 'error')),
    -- What went wrong last, in words the user can be shown. Never secrets.
    last_error text,
    last_synced_at timestamptz,
    -- When the sync worker should look at this connection next.
    next_sync_at   timestamptz NOT NULL DEFAULT now(),

    -- The one-off import of everything already in the service, which only
    -- runs after the user has agreed to it.
    backfill_status       text NOT NULL DEFAULT 'not_requested'
                          CHECK (backfill_status IN ('not_requested', 'running', 'done', 'failed', 'cancelled')),
    backfill_consented_at timestamptz,
    -- How many of the service's activities, oldest first, have been handled.
    backfill_cursor       integer NOT NULL DEFAULT 0,
    backfill_imported     integer NOT NULL DEFAULT 0,
    backfill_skipped      integer NOT NULL DEFAULT 0,
    backfill_failed       integer NOT NULL DEFAULT 0,

    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, provider)
);

CREATE INDEX provider_connections_next_sync_idx ON provider_connections (next_sync_at);

-- Every activity a connection has handled, whatever came of it, so it is
-- never downloaded twice: also the ones skipped as duplicates, without a GPS
-- track, or deleted by the user afterwards.
CREATE TABLE provider_activities (
    connection_id uuid        NOT NULL REFERENCES provider_connections (id) ON DELETE CASCADE,
    external_id   text        NOT NULL,
    started_at    timestamptz,
    -- The importer's status: imported, already_imported, possible_duplicate,
    -- no_gps_track or failed.
    status        text        NOT NULL,
    activity_id   uuid        REFERENCES activities (id) ON DELETE SET NULL,
    error         text,
    processed_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (connection_id, external_id)
);

CREATE INDEX provider_activities_started_idx ON provider_activities (connection_id, started_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON provider_connections, provider_activities TO trackadoodle_app;

-- +goose Down
DROP TABLE provider_activities;
DROP TABLE provider_connections;
ALTER TABLE activities DROP CONSTRAINT activities_user_source_external_id_key;
ALTER TABLE activities DROP COLUMN external_id;
