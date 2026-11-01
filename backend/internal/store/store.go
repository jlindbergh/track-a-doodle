// Package store holds the database access for users and activities.
//
// Every activity query takes the owning user's id and filters on it, so a
// caller can never read or change another user's activities.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activity"
	"github.com/jlindbergh/track-a-doodle/backend/internal/gpxfile"
)

// Store wraps a PostgreSQL connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// New connects to the database at databaseURL.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close closes the connection pool.
func (s *Store) Close() {
	s.pool.Close()
}

// User is an account in the application.
type User struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

// EnsureUserByEmail returns the user with the given email, creating it if needed.
func (s *Store) EnsureUserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (email) VALUES ($1)
		ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id, email, display_name`, email,
	).Scan(&u.ID, &u.Email, &u.DisplayName)
	if err != nil {
		return User{}, fmt.Errorf("ensure user %q: %w", email, err)
	}
	return u, nil
}

// NewActivity is an activity about to be stored.
type NewActivity struct {
	Activity       gpxfile.Activity
	SourceFilename string
	ContentSHA256  string
}

// InsertStatus is the outcome of InsertActivity.
type InsertStatus int

const (
	// Inserted means the activity was stored.
	Inserted InsertStatus = iota
	// SameFile means the user has already imported a byte-identical file.
	SameFile
	// PossibleDuplicate means an existing activity matches the DuplicateRule.
	PossibleDuplicate
)

func (s InsertStatus) String() string {
	switch s {
	case Inserted:
		return "Inserted"
	case SameFile:
		return "SameFile"
	case PossibleDuplicate:
		return "PossibleDuplicate"
	}
	return fmt.Sprintf("InsertStatus(%d)", int(s))
}

// InsertResult describes what InsertActivity did.
type InsertResult struct {
	Status InsertStatus
	// New describes the activity that was to be inserted. Its ID is set if it was.
	New activity.Facts
	// Existing is the already stored activity it matched, if any.
	Existing *activity.Facts
	// Comparison is how New and Existing differ, for PossibleDuplicate.
	Comparison *activity.Comparison
}

// InsertActivity stores an activity for userID unless the user already has
// it. Byte-identical files are always rejected. Other activities are checked
// against rule; pass a nil rule to store possible duplicates anyway.
func (s *Store) InsertActivity(ctx context.Context, userID string, na NewActivity, rule *activity.DuplicateRule) (InsertResult, error) {
	a := na.Activity
	res := InsertResult{New: activity.Facts{Name: a.Name, Sport: a.Sport, StartedAt: a.StartedAt, DurationS: a.DurationS}}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return InsertResult{}, fmt.Errorf("begin import: %w", err)
	}
	defer tx.Rollback(ctx)

	// Serialize imports per user, so two concurrent uploads of the same
	// activity cannot both pass the duplicate checks.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, userID); err != nil {
		return InsertResult{}, fmt.Errorf("lock user imports: %w", err)
	}

	existing, err := scanFacts(tx.QueryRow(ctx, `
		SELECT `+factsColumns+`
		  FROM activities
		 WHERE user_id = $1 AND content_sha256 = $2`,
		userID, na.ContentSHA256))
	if err != nil {
		return InsertResult{}, fmt.Errorf("find same file: %w", err)
	}
	if existing != nil {
		// A byte-identical file has the same track, and so the same distance.
		res.New.DistanceM = existing.DistanceM
		res.Status, res.Existing = SameFile, existing
		return res, nil
	}

	// Insert before looking for duplicates: distance_m is a generated column,
	// so the database is the only place that measures activities. A possible
	// duplicate is rolled back.
	if err := tx.QueryRow(ctx, `
		INSERT INTO activities
			(user_id, name, sport, started_at, duration_s, source, source_filename, content_sha256, geom)
		VALUES ($1, $2, $3, $4, $5, 'gpx', $6, $7, ST_GeomFromText($8, 4326))
		RETURNING id, distance_m`,
		userID, a.Name, a.Sport, a.StartedAt, a.DurationS, na.SourceFilename, na.ContentSHA256, a.WKT(),
	).Scan(&res.New.ID, &res.New.DistanceM); err != nil {
		return InsertResult{}, fmt.Errorf("insert activity: %w", err)
	}

	if rule != nil {
		existing, cmp, err := findDuplicate(ctx, tx, userID, res.New, *rule)
		if err != nil {
			return InsertResult{}, err
		}
		if existing != nil {
			res.New.ID = ""
			res.Status, res.Existing, res.Comparison = PossibleDuplicate, existing, &cmp
			return res, nil
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return InsertResult{}, fmt.Errorf("commit import: %w", err)
	}
	res.Status = Inserted
	return res, nil
}

// findDuplicate returns the activity of userID, other than f itself, that is
// a possible duplicate of f by rule and starts closest to it, together with
// how they compare. It returns nil if there is none.
func findDuplicate(ctx context.Context, tx pgx.Tx, userID string, f activity.Facts, rule activity.DuplicateRule) (*activity.Facts, activity.Comparison, error) {
	if f.StartedAt == nil {
		return nil, activity.Comparison{}, nil
	}
	// The start time window only narrows down the candidates;
	// rule.Matches decides.
	rows, err := tx.Query(ctx, `
		SELECT `+factsColumns+`
		  FROM activities
		 WHERE user_id = $1
		   AND id <> $2
		   AND started_at BETWEEN $3::timestamptz - make_interval(secs => $4)
		                      AND $3::timestamptz + make_interval(secs => $4)
		 ORDER BY abs(extract(epoch FROM started_at - $3::timestamptz))`,
		userID, f.ID, f.StartedAt, rule.StartTolerance.Seconds())
	if err != nil {
		return nil, activity.Comparison{}, fmt.Errorf("find possible duplicates: %w", err)
	}
	candidates, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (activity.Facts, error) {
		var c activity.Facts
		err := row.Scan(factsFields(&c)...)
		return c, err
	})
	if err != nil {
		return nil, activity.Comparison{}, fmt.Errorf("find possible duplicates: %w", err)
	}
	for _, c := range candidates {
		if cmp := activity.Compare(f, c); rule.Matches(cmp) {
			return &c, cmp, nil
		}
	}
	return nil, activity.Comparison{}, nil
}

// factsColumns selects the columns of activity.Facts, in the order factsFields
// scans them.
const factsColumns = `id, name, sport, started_at, duration_s, distance_m`

// factsFields returns the scan destinations for factsColumns.
func factsFields(f *activity.Facts) []any {
	return []any{&f.ID, &f.Name, &f.Sport, &f.StartedAt, &f.DurationS, &f.DistanceM}
}

// scanFacts scans one row of factsColumns. It returns nil if there is no row.
func scanFacts(row pgx.Row) (*activity.Facts, error) {
	var f activity.Facts
	err := row.Scan(factsFields(&f)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ListActivities returns userID's activities, newest first.
func (s *Store) ListActivities(ctx context.Context, userID string) ([]activity.Summary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+factsColumns+`,
		       ST_XMin(geom), ST_YMin(geom), ST_XMax(geom), ST_YMax(geom),
		       source_filename, imported_at
		  FROM activities
		 WHERE user_id = $1
		 ORDER BY started_at DESC NULLS LAST, imported_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list activities: %w", err)
	}
	defer rows.Close()

	list := []activity.Summary{}
	for rows.Next() {
		var a activity.Summary
		fields := append(factsFields(&a.Facts), &a.BBox[0], &a.BBox[1], &a.BBox[2], &a.BBox[3],
			&a.SourceFilename, &a.ImportedAt)
		if err := rows.Scan(fields...); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list activities: %w", err)
	}
	return list, nil
}

// DeleteActivity deletes userID's activity with the given id. It reports
// whether there was one: an activity that belongs to someone else is never
// deleted and is reported the same way as one that doesn't exist.
func (s *Store) DeleteActivity(ctx context.Context, userID, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM activities WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, fmt.Errorf("delete activity: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
