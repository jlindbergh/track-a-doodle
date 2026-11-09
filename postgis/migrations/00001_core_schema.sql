-- Core schema: users and activities.
--
-- The first three migrations were init scripts before goose was added, so
-- they only create what is missing. Databases created by those scripts
-- adopt them without losing data.

-- +goose Up

CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS users (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Always stored trimmed and lowercased by the backend.
    email        text        NOT NULL UNIQUE,
    display_name text        NOT NULL DEFAULT '',
    -- argon2id hash in PHC format. NULL for users who can't sign in with a
    -- password (dev mode, and later OIDC-only users).
    password_hash     text,
    -- NULL until the user has confirmed the address. Unverified users can't
    -- sign in and are purged after a few days.
    email_verified_at timestamptz,
    -- Filled in once OIDC login (Zitadel/Strava) is in place.
    oidc_issuer  text,
    oidc_subject text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (oidc_issuer, oidc_subject)
);

-- Databases created by the init scripts before email + password accounts
-- have users without these columns, and the CREATE above leaves them as is.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS password_hash     text,
    ADD COLUMN IF NOT EXISTS email_verified_at timestamptz;

CREATE TABLE IF NOT EXISTS activities (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name            text        NOT NULL,
    sport           text        NOT NULL DEFAULT 'unknown',
    started_at      timestamptz,
    duration_s      integer,
    source          text        NOT NULL DEFAULT 'gpx',
    source_filename text,
    -- SHA-256 of the uploaded file, so re-importing the same file is a no-op.
    content_sha256  text        NOT NULL,
    imported_at     timestamptz NOT NULL DEFAULT now(),

    -- Full-resolution track, one line per GPX track segment.
    geom            geometry(MultiLineString, 4326) NOT NULL,

    -- Derived columns used by tiles and listings.
    distance_m      double precision GENERATED ALWAYS AS (ST_Length(geom::geography)) STORED,
    start_pt        geometry(Point, 4326) GENERATED ALWAYS AS (ST_StartPoint(ST_GeometryN(geom, 1))) STORED,
    geom_3857       geometry(MultiLineString, 3857) GENERATED ALWAYS AS (ST_Transform(geom, 3857)) STORED,
    -- Simplified for mid zooms (z10-12) and overview zooms (z0-9). Tolerance in metres (EPSG:3857);
    -- preserveCollapsed keeps very short tracks visible instead of dropping them.
    geom_mid        geometry(Geometry, 3857) GENERATED ALWAYS AS (ST_Simplify(ST_Transform(geom, 3857), 10, true)) STORED,
    geom_lo         geometry(Geometry, 3857) GENERATED ALWAYS AS (ST_Simplify(ST_Transform(geom, 3857), 100, true)) STORED,

    UNIQUE (user_id, content_sha256)
);

CREATE INDEX IF NOT EXISTS activities_user_started_idx ON activities (user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS activities_geom_3857_idx ON activities USING gist (geom_3857);

GRANT SELECT, INSERT, UPDATE, DELETE ON users, activities TO trackadoodle_app;
