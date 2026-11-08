-- Email + password accounts: one-time email tokens and sessions.
--
-- Tokens are never stored in the clear: the backend sends or sets a random
-- token and keeps only its SHA-256, so reading these tables gives no way in.

-- +goose Up

-- One-time tokens sent by email. Only email verification exists so far;
-- purpose leaves room for password reset.
CREATE TABLE IF NOT EXISTS email_tokens (
    token_hash bytea       PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    text        NOT NULL CHECK (purpose IN ('verify')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS email_tokens_user_purpose_idx ON email_tokens (user_id, purpose);

-- Every email sent to an address, kept for a day to limit how often an
-- address can be mailed. Tokens can't be used for that, since only the
-- latest one is kept.
CREATE TABLE IF NOT EXISTS email_sends (
    user_id uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    sent_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS email_sends_user_sent_idx ON email_sends (user_id, sent_at);

-- Signed-in browsers. The cookie holds the token; this table its hash.
CREATE TABLE IF NOT EXISTS sessions (
    token_hash bytea       PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions (user_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON email_tokens, email_sends, sessions TO trackadoodle_app;
