package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Connection statuses.
const (
	ConnectionActive     = "active"
	ConnectionInvalidKey = "invalid_key"
	ConnectionKeyExpired = "key_expired"
	ConnectionError      = "error"
)

// Backfill statuses.
const (
	BackfillNotRequested = "not_requested"
	BackfillRunning      = "running"
	BackfillDone         = "done"
	BackfillFailed       = "failed"
	BackfillCancelled    = "cancelled"
)

// Connection is a user's connection to an activity service, with its
// credentials still encrypted (see package secretbox).
type Connection struct {
	ID       string
	UserID   string
	Provider string

	APIKeyEnc            []byte
	KeyExpiresAt         *time.Time
	AccessTokenEnc       []byte
	AccessTokenExpiresAt *time.Time

	Status       string
	LastError    *string
	LastSyncedAt *time.Time
	NextSyncAt   time.Time

	BackfillStatus      string
	BackfillConsentedAt *time.Time
	// BackfillCursor is how many of the service's activities, oldest first,
	// the backfill has gone through: the offset of its next page.
	BackfillCursor int

	CreatedAt time.Time
}

const connectionColumns = `id, user_id, provider, api_key_enc, key_expires_at, access_token_enc,
	access_token_expires_at, status, last_error, last_synced_at, next_sync_at, backfill_status,
	backfill_consented_at, backfill_cursor, created_at`

func scanConnection(row pgx.Row) (Connection, error) {
	var c Connection
	err := row.Scan(&c.ID, &c.UserID, &c.Provider, &c.APIKeyEnc, &c.KeyExpiresAt, &c.AccessTokenEnc,
		&c.AccessTokenExpiresAt, &c.Status, &c.LastError, &c.LastSyncedAt, &c.NextSyncAt, &c.BackfillStatus,
		&c.BackfillConsentedAt, &c.BackfillCursor, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	return c, err
}

// Credentials are a connection's encrypted key and access token.
type Credentials struct {
	APIKeyEnc            []byte
	KeyExpiresAt         *time.Time
	AccessTokenEnc       []byte
	AccessTokenExpiresAt time.Time
}

// SaveConnection connects userID to provider with creds, or replaces the
// credentials of their existing connection. Either way the connection
// becomes active and is synced as soon as possible; the backfill is left as
// it was, so a backfill held up by an expired key carries on.
func (s *Store) SaveConnection(ctx context.Context, userID, provider string, creds Credentials) (Connection, error) {
	c, err := scanConnection(s.pool.QueryRow(ctx, `
		INSERT INTO provider_connections
			(user_id, provider, api_key_enc, key_expires_at, access_token_enc, access_token_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, provider) DO UPDATE SET
			api_key_enc = EXCLUDED.api_key_enc,
			key_expires_at = EXCLUDED.key_expires_at,
			access_token_enc = EXCLUDED.access_token_enc,
			access_token_expires_at = EXCLUDED.access_token_expires_at,
			status = 'active',
			last_error = NULL,
			next_sync_at = now()
		RETURNING `+connectionColumns,
		userID, provider, creds.APIKeyEnc, creds.KeyExpiresAt, creds.AccessTokenEnc, creds.AccessTokenExpiresAt))
	if err != nil {
		return Connection{}, fmt.Errorf("save %s connection: %w", provider, err)
	}
	return c, nil
}

// ConnectionsForUser returns userID's connections, by provider.
func (s *Store) ConnectionsForUser(ctx context.Context, userID string) ([]Connection, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+connectionColumns+`
		  FROM provider_connections
		 WHERE user_id = $1
		 ORDER BY provider`, userID)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	conns, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Connection, error) { return scanConnection(row) })
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	return conns, nil
}

// ConnectionForUser returns userID's connection to provider, or ErrNotFound.
func (s *Store) ConnectionForUser(ctx context.Context, userID, provider string) (Connection, error) {
	c, err := scanConnection(s.pool.QueryRow(ctx, `
		SELECT `+connectionColumns+`
		  FROM provider_connections
		 WHERE user_id = $1 AND provider = $2`, userID, provider))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Connection{}, fmt.Errorf("get %s connection: %w", provider, err)
	}
	return c, err
}

// DeleteConnection deletes userID's connection to provider, with its
// credentials and the record of what it synced. The activities it imported
// stay. It reports whether there was such a connection.
func (s *Store) DeleteConnection(ctx context.Context, userID, provider string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM provider_connections WHERE user_id = $1 AND provider = $2`, userID, provider)
	if err != nil {
		return false, fmt.Errorf("delete %s connection: %w", provider, err)
	}
	return tag.RowsAffected() > 0, nil
}

// StartBackfill records that userID agreed to import everything from
// provider, and starts the backfill from the beginning, unless it is already
// running. Activities handled before are not downloaded again. It returns
// the connection, or ErrNotFound.
func (s *Store) StartBackfill(ctx context.Context, userID, provider string) (Connection, error) {
	c, err := scanConnection(s.pool.QueryRow(ctx, `
		UPDATE provider_connections SET
			backfill_status = 'running',
			backfill_consented_at = now(),
			backfill_cursor = 0,
			next_sync_at = now()
		 WHERE user_id = $1 AND provider = $2 AND backfill_status <> 'running'
		RETURNING `+connectionColumns, userID, provider))
	if errors.Is(err, ErrNotFound) {
		// Already running, or no connection.
		return s.ConnectionForUser(ctx, userID, provider)
	}
	if err != nil {
		return Connection{}, fmt.Errorf("start %s backfill: %w", provider, err)
	}
	return c, nil
}

// CancelBackfill stops userID's running backfill from provider. What has
// been imported stays. It returns the connection, or ErrNotFound.
func (s *Store) CancelBackfill(ctx context.Context, userID, provider string) (Connection, error) {
	if _, err := s.pool.Exec(ctx, `
		UPDATE provider_connections SET backfill_status = 'cancelled'
		 WHERE user_id = $1 AND provider = $2 AND backfill_status = 'running'`, userID, provider); err != nil {
		return Connection{}, fmt.Errorf("cancel %s backfill: %w", provider, err)
	}
	return s.ConnectionForUser(ctx, userID, provider)
}

// The methods below are for the sync worker, which works on connections by
// id. It only gets ids from ClaimDueConnection, and imports activities for
// the connection's own UserID.

// ClaimDueConnection returns a connection that is due to be synced and can
// be, or ErrNotFound if there is none. The connection is not offered again
// for lease, so that another worker leaves it alone; FinishSync sets when it
// is next due.
func (s *Store) ClaimDueConnection(ctx context.Context, lease time.Duration) (Connection, error) {
	c, err := scanConnection(s.pool.QueryRow(ctx, `
		UPDATE provider_connections
		   SET next_sync_at = now() + make_interval(secs => $1)
		 WHERE id = (
			SELECT id FROM provider_connections
			 WHERE next_sync_at <= now() AND status IN ('active', 'error')
			 ORDER BY next_sync_at
			 LIMIT 1
			 FOR UPDATE SKIP LOCKED)
		RETURNING `+connectionColumns, lease.Seconds()))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Connection{}, fmt.Errorf("claim connection: %w", err)
	}
	return c, err
}

// SaveAccessToken stores a new encrypted access token for a connection.
func (s *Store) SaveAccessToken(ctx context.Context, connectionID string, tokenEnc []byte, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE provider_connections SET access_token_enc = $2, access_token_expires_at = $3
		 WHERE id = $1`, connectionID, tokenEnc, expiresAt)
	if err != nil {
		return fmt.Errorf("save access token: %w", err)
	}
	return nil
}

// SyncResult is how a sync of a connection ended.
type SyncResult struct {
	Status string
	// LastError is shown to the user; nil clears it.
	LastError *string
	// Synced is true when the sync got through, to record when.
	Synced     bool
	NextSyncAt time.Time
}

// FinishSync records how a sync of a connection ended and when it is due
// next.
func (s *Store) FinishSync(ctx context.Context, connectionID string, r SyncResult) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE provider_connections SET
			status = $2,
			last_error = $3,
			last_synced_at = CASE WHEN $4 THEN now() ELSE last_synced_at END,
			next_sync_at = $5
		 WHERE id = $1`, connectionID, r.Status, r.LastError, r.Synced, r.NextSyncAt)
	if err != nil {
		return fmt.Errorf("finish sync: %w", err)
	}
	return nil
}

// SaveBackfillCursor moves a running backfill on to cursor, the offset of
// its next page. It reports false, and changes nothing, if the backfill is
// no longer running, e.g. because the user cancelled it.
func (s *Store) SaveBackfillCursor(ctx context.Context, connectionID string, cursor int) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE provider_connections SET backfill_cursor = $2
		 WHERE id = $1 AND backfill_status = 'running'`, connectionID, cursor)
	if err != nil {
		return false, fmt.Errorf("save backfill cursor: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// FinishBackfill ends a running backfill with status (BackfillDone or
// BackfillFailed). A cancelled backfill stays cancelled.
func (s *Store) FinishBackfill(ctx context.Context, connectionID, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE provider_connections SET backfill_status = $2
		 WHERE id = $1 AND backfill_status = 'running'`, connectionID, status)
	if err != nil {
		return fmt.Errorf("finish backfill: %w", err)
	}
	return nil
}

// BackfillRunning reports whether a connection's backfill is still running.
func (s *Store) BackfillRunning(ctx context.Context, connectionID string) (bool, error) {
	var running bool
	err := s.pool.QueryRow(ctx, `
		SELECT backfill_status = 'running' FROM provider_connections WHERE id = $1`, connectionID).Scan(&running)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get backfill status: %w", err)
	}
	return running, nil
}

// ProviderActivity is what came of one activity a connection handled.
type ProviderActivity struct {
	ExternalID string
	StartedAt  *time.Time
	// Status is the importer's status.
	Status     string
	ActivityID string
	Error      string
}

// HandledProviderActivities returns which of externalIDs a connection has
// handled before.
func (s *Store) HandledProviderActivities(ctx context.Context, connectionID string, externalIDs []string) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT external_id FROM provider_activities
		 WHERE connection_id = $1 AND external_id = ANY($2)`, connectionID, externalIDs)
	if err != nil {
		return nil, fmt.Errorf("find handled activities: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("find handled activities: %w", err)
	}
	handled := make(map[string]bool, len(ids))
	for _, id := range ids {
		handled[id] = true
	}
	return handled, nil
}

// RecordProviderActivity records what came of an activity a connection
// handled, so it isn't downloaded again.
func (s *Store) RecordProviderActivity(ctx context.Context, connectionID string, a ProviderActivity) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO provider_activities (connection_id, external_id, started_at, status, activity_id, error)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (connection_id, external_id) DO UPDATE SET
			started_at = EXCLUDED.started_at,
			status = EXCLUDED.status,
			activity_id = EXCLUDED.activity_id,
			error = EXCLUDED.error,
			processed_at = now()`,
		connectionID, a.ExternalID, a.StartedAt, a.Status, nullIfEmpty(a.ActivityID), nullIfEmpty(a.Error))
	if err != nil {
		return fmt.Errorf("record handled activity: %w", err)
	}
	return nil
}

// ProviderActivityCounts is what came of the activities a connection has
// handled, counted from the records RecordProviderActivity keeps.
type ProviderActivityCounts struct {
	Imported int
	// Skipped are already imported, possible duplicates or without a GPS
	// track.
	Skipped int
	Failed  int
	// LatestBefore is the start time of the latest activity handled that
	// started before the time CountProviderActivities was given, or nil.
	LatestBefore *time.Time
}

// CountProviderActivities counts what came of the activities a connection
// has handled, and finds the latest one that started before before.
func (s *Store) CountProviderActivities(ctx context.Context, connectionID string, before time.Time) (ProviderActivityCounts, error) {
	var c ProviderActivityCounts
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'imported'),
		       count(*) FILTER (WHERE status NOT IN ('imported', 'failed')),
		       count(*) FILTER (WHERE status = 'failed'),
		       max(started_at) FILTER (WHERE started_at < $2)
		  FROM provider_activities WHERE connection_id = $1`, connectionID, before).
		Scan(&c.Imported, &c.Skipped, &c.Failed, &c.LatestBefore)
	if err != nil {
		return ProviderActivityCounts{}, fmt.Errorf("count handled activities: %w", err)
	}
	return c, nil
}

// LatestProviderActivityStart returns the start time of the latest activity
// a connection has handled, or nil if none.
func (s *Store) LatestProviderActivityStart(ctx context.Context, connectionID string) (*time.Time, error) {
	var t *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT max(started_at) FROM provider_activities WHERE connection_id = $1`, connectionID).Scan(&t)
	if err != nil {
		return nil, fmt.Errorf("latest handled activity: %w", err)
	}
	return t, nil
}
