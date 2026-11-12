-- Records which activity a synced activity was found to duplicate, and
-- whether that was decided from the service's summary of it, without
-- downloading its file.

-- +goose Up
ALTER TABLE provider_activities
    -- The user's activity this one matched, for already_imported and
    -- possible_duplicate.
    ADD COLUMN matched_activity_id uuid REFERENCES activities (id) ON DELETE SET NULL,
    -- 'file' when the downloaded file was imported or compared, 'summary'
    -- when the service's summary matched an existing activity and nothing
    -- was downloaded.
    ADD COLUMN decided_from text NOT NULL DEFAULT 'file' CHECK (decided_from IN ('file', 'summary'));

-- +goose Down
ALTER TABLE provider_activities
    DROP COLUMN matched_activity_id,
    DROP COLUMN decided_from;
