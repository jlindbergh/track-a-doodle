// Package activitysync connects users to activity services and keeps their
// activities in sync: for now Garmin Connect, through Garmin's Personal API
// (see package garmin and docs/garmin.md).
//
// A user connects by giving their personal API key, which is checked with
// Garmin and stored encrypted. From then on a background worker (Run) polls
// for new activities. Importing everything already in Garmin Connect, the
// backfill, only starts once the user has agreed to it (StartBackfill).
//
// Every activity is imported for the connection's own user, and a user can
// only see and change their own connections.
package activitysync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/importer"
	"github.com/jlindbergh/track-a-doodle/backend/internal/provider/garmin"
	"github.com/jlindbergh/track-a-doodle/backend/internal/secretbox"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

// ProviderGarmin is the provider id of Garmin Connect.
const ProviderGarmin = "garmin"

// Errors returned to users.
var (
	// ErrEmptyKey means no API key was given.
	ErrEmptyKey = errors.New("enter your Garmin personal API key")
	// ErrKeyRejected means Garmin rejected the API key.
	ErrKeyRejected = errors.New("Garmin rejected the API key; check that you copied all of it and that it hasn't expired or been deleted")
	// ErrNotConnected means the user has no such connection.
	ErrNotConnected = errors.New("not connected")
	// ErrGarminUnavailable means Garmin couldn't be reached to check a key.
	ErrGarminUnavailable = errors.New("could not reach Garmin to check the key; try again in a while")
)

// gpsFields makes Garmin's activity search leave out activities without
// GPS. Checked against the real API: it skips e.g. strength workouts.
var gpsFields = []string{"startLatitude"}

// excludedSubTypes are activity types left out of syncs even when Garmin
// has a start position for them: indoor and virtual activities, whose
// "tracks" are a treadmill's or a game's. Unknown keys may make Garmin
// reject the search, so only add type keys seen in Garmin's data.
var excludedSubTypes = []string{"treadmill_running", "indoor_cycling", "virtual_ride", "virtual_run"}

// GarminAPI is what the sync needs from Garmin; *garmin.Client implements it.
type GarminAPI interface {
	Token(ctx context.Context, apiKey string) (garmin.Token, error)
	SearchActivities(ctx context.Context, token string, p garmin.SearchParams) ([]garmin.ActivitySummary, error)
	DownloadFIT(ctx context.Context, token string, activityID int64) ([]byte, error)
}

// Store is the database access the sync needs; *store.Store implements it.
type Store interface {
	SaveConnection(ctx context.Context, userID, provider string, creds store.Credentials) (store.Connection, error)
	ConnectionsForUser(ctx context.Context, userID string) ([]store.Connection, error)
	ConnectionForUser(ctx context.Context, userID, provider string) (store.Connection, error)
	DeleteConnection(ctx context.Context, userID, provider string) (bool, error)
	StartBackfill(ctx context.Context, userID, provider string) (store.Connection, error)
	CancelBackfill(ctx context.Context, userID, provider string) (store.Connection, error)

	ClaimDueConnection(ctx context.Context, lease time.Duration) (store.Connection, error)
	SaveAccessToken(ctx context.Context, connectionID string, tokenEnc []byte, expiresAt time.Time) error
	FinishSync(ctx context.Context, connectionID string, r store.SyncResult) error
	SaveBackfillProgress(ctx context.Context, connectionID string, p store.BackfillProgress) (bool, error)
	FinishBackfill(ctx context.Context, connectionID, status string) error
	BackfillRunning(ctx context.Context, connectionID string) (bool, error)
	HandledProviderActivities(ctx context.Context, connectionID string, externalIDs []string) (map[string]bool, error)
	RecordProviderActivity(ctx context.Context, connectionID string, a store.ProviderActivity) error
	LatestProviderActivityStart(ctx context.Context, connectionID string) (*time.Time, error)
}

// Importer imports activity files; *importer.Importer implements it.
type Importer interface {
	Import(ctx context.Context, userID, filename string, data []byte, opts importer.Options) (importer.Result, error)
}

// Config configures a Service. Zero durations and sizes get the defaults.
type Config struct {
	Store    Store
	Importer Importer
	// Box encrypts the API keys and access tokens.
	Box    *secretbox.Box
	Logger *slog.Logger
	// NewGarmin returns a Garmin client that logs to log. Defaults to
	// garmin.New with Garmin's address.
	NewGarmin func(log *slog.Logger) GarminAPI

	// PollInterval is how often a connection is checked for new
	// activities. Default 30 minutes.
	PollInterval time.Duration
	// RetryInterval is how long to wait after a failed sync. Default 15
	// minutes.
	RetryInterval time.Duration
	// WorkBudget is how long one sync may go on before it lets other
	// connections have a turn; a backfill carries on right after. Default
	// 10 minutes.
	WorkBudget time.Duration
	// Lease keeps other workers away from a connection being synced. It
	// must be longer than WorkBudget plus a page of downloads. Default 30
	// minutes.
	Lease time.Duration
	// TickInterval is how often the worker looks for due connections.
	// Default 1 minute.
	TickInterval time.Duration
	// PageSize is how many activities are fetched per search. Default 99,
	// Garmin's own default.
	PageSize int
}

// Service connects users to Garmin and syncs their activities.
type Service struct {
	store     Store
	importer  Importer
	box       *secretbox.Box
	log       *slog.Logger
	newGarmin func(*slog.Logger) GarminAPI

	pollInterval, retryInterval, workBudget, lease, tick time.Duration
	pageSize                                             int
}

// New returns a Service configured by cfg.
func New(cfg Config) *Service {
	s := &Service{
		store:         cfg.Store,
		importer:      cfg.Importer,
		box:           cfg.Box,
		log:           cfg.Logger,
		newGarmin:     cfg.NewGarmin,
		pollInterval:  orDefault(cfg.PollInterval, 30*time.Minute),
		retryInterval: orDefault(cfg.RetryInterval, 15*time.Minute),
		workBudget:    orDefault(cfg.WorkBudget, 10*time.Minute),
		lease:         orDefault(cfg.Lease, 30*time.Minute),
		tick:          orDefault(cfg.TickInterval, time.Minute),
		pageSize:      cfg.PageSize,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.newGarmin == nil {
		s.newGarmin = func(log *slog.Logger) GarminAPI { return garmin.New(garmin.Config{Logger: log}) }
	}
	if s.pageSize <= 0 {
		s.pageSize = 99
	}
	return s
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Status is what a user is shown about one of their connections. It never
// holds credentials.
type Status struct {
	Provider     string     `json:"provider"`
	Status       string     `json:"status"`
	LastError    *string    `json:"lastError"`
	KeyExpiresAt *time.Time `json:"keyExpiresAt"`
	ConnectedAt  time.Time  `json:"connectedAt"`
	LastSyncedAt *time.Time `json:"lastSyncedAt"`
	NextSyncAt   time.Time  `json:"nextSyncAt"`
	Backfill     Backfill   `json:"backfill"`
}

// Backfill is the progress of importing everything from the service.
type Backfill struct {
	Status      string     `json:"status"`
	ConsentedAt *time.Time `json:"consentedAt"`
	// Checked is how many of the service's activities with GPS have been
	// looked at, oldest first.
	Checked  int `json:"checked"`
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
}

func statusOf(c store.Connection) Status {
	return Status{
		Provider:     c.Provider,
		Status:       c.Status,
		LastError:    c.LastError,
		KeyExpiresAt: c.KeyExpiresAt,
		ConnectedAt:  c.CreatedAt,
		LastSyncedAt: c.LastSyncedAt,
		NextSyncAt:   c.NextSyncAt,
		Backfill: Backfill{
			Status:      c.BackfillStatus,
			ConsentedAt: c.BackfillConsentedAt,
			Checked:     c.BackfillCursor,
			Imported:    c.BackfillImported,
			Skipped:     c.BackfillSkipped,
			Failed:      c.BackfillFailed,
		},
	}
}

// keyContext and tokenContext bind sealed credentials to their user and
// provider (see secretbox.Box.Seal).
func keyContext(userID, provider string) string   { return provider + " api key|" + userID }
func tokenContext(userID, provider string) string { return provider + " access token|" + userID }

// ConnectGarmin checks apiKey with Garmin and connects userID's account, or
// replaces the key of an existing connection. keyExpiresAt is when the user
// says the key expires, if they do.
func (s *Service) ConnectGarmin(ctx context.Context, userID, apiKey string, keyExpiresAt *time.Time) (Status, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return Status{}, ErrEmptyKey
	}
	log := s.log.With("provider", ProviderGarmin, "user_id", userID)
	tok, err := s.newGarmin(log).Token(ctx, apiKey)
	if errors.Is(err, garmin.ErrInvalidKey) {
		log.Info("garmin key rejected when connecting")
		return Status{}, ErrKeyRejected
	}
	if err != nil {
		log.Error("check garmin key", "err", err)
		return Status{}, ErrGarminUnavailable
	}
	c, err := s.store.SaveConnection(ctx, userID, ProviderGarmin, store.Credentials{
		APIKeyEnc:            s.box.Seal([]byte(apiKey), keyContext(userID, ProviderGarmin)),
		KeyExpiresAt:         keyExpiresAt,
		AccessTokenEnc:       s.box.Seal([]byte(tok.Value), tokenContext(userID, ProviderGarmin)),
		AccessTokenExpiresAt: tok.ExpiresAt,
	})
	if err != nil {
		return Status{}, err
	}
	log.Info("garmin connected", "connection_id", c.ID)
	return statusOf(c), nil
}

// Connections returns userID's connections.
func (s *Service) Connections(ctx context.Context, userID string) ([]Status, error) {
	conns, err := s.store.ConnectionsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Status, len(conns))
	for i, c := range conns {
		out[i] = statusOf(c)
	}
	return out, nil
}

// StartBackfill starts importing everything userID has in provider, which
// they have agreed to. It returns ErrNotConnected if they aren't connected.
func (s *Service) StartBackfill(ctx context.Context, userID, provider string) (Status, error) {
	c, err := s.store.StartBackfill(ctx, userID, provider)
	if errors.Is(err, store.ErrNotFound) {
		return Status{}, ErrNotConnected
	}
	if err != nil {
		return Status{}, err
	}
	s.log.Info("backfill started", "provider", provider, "user_id", userID, "connection_id", c.ID)
	return statusOf(c), nil
}

// CancelBackfill stops userID's backfill from provider.
func (s *Service) CancelBackfill(ctx context.Context, userID, provider string) (Status, error) {
	c, err := s.store.CancelBackfill(ctx, userID, provider)
	if errors.Is(err, store.ErrNotFound) {
		return Status{}, ErrNotConnected
	}
	if err != nil {
		return Status{}, err
	}
	s.log.Info("backfill cancelled", "provider", provider, "user_id", userID, "connection_id", c.ID)
	return statusOf(c), nil
}

// Disconnect deletes userID's connection to provider and its credentials.
// Imported activities stay. It returns ErrNotConnected if there was none.
func (s *Service) Disconnect(ctx context.Context, userID, provider string) error {
	deleted, err := s.store.DeleteConnection(ctx, userID, provider)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrNotConnected
	}
	s.log.Info("disconnected", "provider", provider, "user_id", userID)
	return nil
}

// Run syncs due connections every TickInterval until ctx is done.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		if err := s.syncDue(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("sync due connections", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// syncDue syncs connections until none is due.
func (s *Service) syncDue(ctx context.Context) error {
	for ctx.Err() == nil {
		c, err := s.store.ClaimDueConnection(ctx, s.lease)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		s.syncConnection(ctx, c)
	}
	return ctx.Err()
}

// syncConnection syncs one claimed connection and records how it went.
func (s *Service) syncConnection(ctx context.Context, c store.Connection) {
	log := s.log.With("provider", c.Provider, "connection_id", c.ID, "user_id", c.UserID)
	r := &syncRun{
		s:        s,
		conn:     c,
		log:      log,
		api:      s.newGarmin(log),
		deadline: time.Now().Add(s.workBudget),
	}
	started := time.Now()
	res := r.sync(ctx)
	log.Info("sync finished", "status", res.Status, "backfill", r.backfilling, "imported", r.counts.Imported,
		"skipped", r.counts.Skipped, "failed", r.counts.Failed, "duration", time.Since(started), "next_sync_at", res.NextSyncAt)

	// Record the outcome even when shutting down, so the connection is
	// picked up again on restart instead of waiting out its lease.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.store.FinishSync(finishCtx, c.ID, res); err != nil {
		log.Error("record sync result", "err", err)
	}
}

// errOutOfTime means a sync used up its WorkBudget and should continue later.
var errOutOfTime = errors.New("work budget used up")

// errKeyUnreadable means a stored key cannot be decrypted, e.g. because
// SECRETS_KEY has changed.
var errKeyUnreadable = errors.New("stored API key cannot be decrypted")

// syncRun is one sync of one connection.
type syncRun struct {
	s           *Service
	conn        store.Connection
	log         *slog.Logger
	api         GarminAPI
	token       string
	deadline    time.Time
	backfilling bool
	counts      store.BackfillProgress
}

func (r *syncRun) sync(ctx context.Context) store.SyncResult {
	now := time.Now()
	if exp := r.conn.KeyExpiresAt; exp != nil && now.After(*exp) {
		return r.failure(ctx, ErrKeyRejected)
	}
	if err := r.loadToken(ctx); err != nil {
		return r.failure(ctx, err)
	}

	more := false
	var err error
	if r.conn.BackfillStatus == store.BackfillRunning {
		r.backfilling = true
		more, err = r.backfill(ctx)
	} else {
		err = r.poll(ctx)
	}
	if errors.Is(err, errOutOfTime) {
		more, err = true, nil
	}
	if err != nil {
		return r.failure(ctx, err)
	}
	next := time.Now().Add(r.s.pollInterval)
	if more {
		next = time.Now()
	}
	return store.SyncResult{Status: store.ConnectionActive, Synced: true, NextSyncAt: next}
}

// failure turns an error that ended a sync into its result, with a message
// for the user.
func (r *syncRun) failure(ctx context.Context, err error) store.SyncResult {
	now := time.Now()
	msg := func(s string) *string { return &s }
	var se *garmin.StatusError
	switch {
	case ctx.Err() != nil:
		// Shutting down: try again as soon as we're back.
		return store.SyncResult{Status: r.conn.Status, LastError: r.conn.LastError, NextSyncAt: now}
	case errors.Is(err, ErrKeyRejected):
		status := store.ConnectionInvalidKey
		text := "Garmin rejected the API key. Create a new key in Garmin Connect and replace it here."
		if exp := r.conn.KeyExpiresAt; exp != nil && now.After(*exp) {
			status = store.ConnectionKeyExpired
			text = "The Garmin API key has expired. Create a new key in Garmin Connect and replace it here."
		}
		r.log.Warn("garmin key rejected; syncing stops until the key is replaced", "status", status)
		return store.SyncResult{Status: status, LastError: &text, NextSyncAt: now}
	case errors.Is(err, errKeyUnreadable):
		r.log.Error("sync", "err", err)
		return store.SyncResult{Status: store.ConnectionInvalidKey,
			LastError: msg("The stored Garmin API key can no longer be read. Replace the key to carry on."), NextSyncAt: now}
	case errors.As(err, &se) && se.Throttled():
		next := now.Add(max(se.RetryAfter, r.s.retryInterval))
		r.log.Warn("garmin is throttling; sync postponed", "next_sync_at", next, "retry_after", se.RetryAfter)
		return store.SyncResult{Status: store.ConnectionError,
			LastError: msg("Garmin is limiting how fast we can fetch activities. Syncing carries on at " + next.UTC().Format("15:04") + " UTC."), NextSyncAt: next}
	default:
		next := now.Add(r.s.retryInterval)
		r.log.Error("sync failed", "err", err, "next_sync_at", next)
		return store.SyncResult{Status: store.ConnectionError,
			LastError: msg(fmt.Sprintf("Syncing with Garmin failed. It is tried again in %d minutes.", int(r.s.retryInterval.Minutes()))), NextSyncAt: next}
	}
}

// tokenMargin is how long before it expires a stored token is replaced.
const tokenMargin = 10 * time.Minute

// loadToken sets r.token from the stored access token, or a new one if
// that is missing, unreadable or about to expire.
func (r *syncRun) loadToken(ctx context.Context) error {
	c := r.conn
	if c.AccessTokenEnc != nil && c.AccessTokenExpiresAt != nil && time.Until(*c.AccessTokenExpiresAt) > tokenMargin {
		tok, err := r.s.box.Open(c.AccessTokenEnc, tokenContext(c.UserID, c.Provider))
		if err == nil {
			r.token = string(tok)
			return nil
		}
		r.log.Warn("stored access token cannot be decrypted; making a new one")
	}
	return r.newToken(ctx)
}

// newToken exchanges the connection's key for a new access token and saves
// it.
func (r *syncRun) newToken(ctx context.Context) error {
	c := r.conn
	key, err := r.s.box.Open(c.APIKeyEnc, keyContext(c.UserID, c.Provider))
	if err != nil {
		return errKeyUnreadable
	}
	tok, err := r.api.Token(ctx, string(key))
	if errors.Is(err, garmin.ErrInvalidKey) {
		return ErrKeyRejected
	}
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}
	if err := r.s.store.SaveAccessToken(ctx, c.ID, r.s.box.Seal([]byte(tok.Value), tokenContext(c.UserID, c.Provider)), tok.ExpiresAt); err != nil {
		return err
	}
	r.token = tok.Value
	r.log.Info("garmin access token renewed", "expires_at", tok.ExpiresAt)
	return nil
}

// withToken calls f with the access token, and once more with a new token
// if Garmin rejected the old one.
func withToken[T any](ctx context.Context, r *syncRun, f func(token string) (T, error)) (T, error) {
	v, err := f(r.token)
	if !errors.Is(err, garmin.ErrUnauthorized) {
		return v, err
	}
	r.log.Info("garmin rejected the access token; making a new one")
	if err := r.newToken(ctx); err != nil {
		var zero T
		return zero, err
	}
	return f(r.token)
}

func (r *syncRun) search(ctx context.Context, p garmin.SearchParams) ([]garmin.ActivitySummary, error) {
	p.Limit = r.s.pageSize
	p.SortOrder = garmin.SortAscending
	p.RequiredFields = gpsFields
	p.ExcludedSubTypes = excludedSubTypes
	return withToken(ctx, r, func(token string) ([]garmin.ActivitySummary, error) {
		return r.api.SearchActivities(ctx, token, p)
	})
}

// backfill imports pages of the user's activities, oldest first, from the
// saved cursor until all are done, the backfill is cancelled, or the work
// budget is used up. It reports whether there is more to do.
func (r *syncRun) backfill(ctx context.Context) (bool, error) {
	cursor := r.conn.BackfillCursor
	for {
		if time.Now().After(r.deadline) {
			return true, nil
		}
		// The user may have cancelled since the last page.
		if running, err := r.s.store.BackfillRunning(ctx, r.conn.ID); err != nil || !running {
			return false, err
		}
		page, err := r.search(ctx, garmin.SearchParams{Start: cursor})
		if err != nil {
			return false, err
		}
		if len(page) == 0 {
			return false, r.finishBackfill(ctx)
		}

		counts, err := r.importPage(ctx, page, nil)
		r.counts.Imported += counts.Imported
		r.counts.Skipped += counts.Skipped
		r.counts.Failed += counts.Failed
		if err != nil {
			// Keep what was done. The page is fetched again next time and
			// the activities already handled are passed over.
			counts.Cursor = cursor
			if _, saveErr := r.s.store.SaveBackfillProgress(ctx, r.conn.ID, counts); saveErr != nil {
				r.log.Error("save backfill progress", "err", saveErr)
			}
			return false, err
		}
		cursor += len(page)
		counts.Cursor = cursor
		if ok, err := r.s.store.SaveBackfillProgress(ctx, r.conn.ID, counts); err != nil || !ok {
			return false, err
		}
		r.log.Info("backfill page done", "checked", cursor, "page_imported", counts.Imported, "page_skipped", counts.Skipped, "page_failed", counts.Failed)
		if len(page) < r.s.pageSize {
			return false, r.finishBackfill(ctx)
		}
	}
}

func (r *syncRun) finishBackfill(ctx context.Context) error {
	r.log.Info("backfill done")
	return r.s.store.FinishBackfill(ctx, r.conn.ID, store.BackfillDone)
}

// poll imports activities newer than the last one handled. Without a
// finished backfill, only activities from after the user connected are
// imported: the rest is the backfill's job, if they ask for it.
func (r *syncRun) poll(ctx context.Context) error {
	latest, err := r.s.store.LatestProviderActivityStart(ctx, r.conn.ID)
	if err != nil {
		return err
	}
	since := r.conn.CreatedAt
	if latest != nil {
		since = *latest
	}
	var keep func(time.Time) bool
	if r.conn.BackfillStatus != store.BackfillDone {
		connected := r.conn.CreatedAt
		keep = func(started time.Time) bool { return !started.Before(connected) }
	}
	// Garmin filters by local start date; a day's margin covers every
	// time zone.
	p := garmin.SearchParams{StartDate: since.Add(-24 * time.Hour).UTC().Format(time.DateOnly)}
	for {
		page, err := r.search(ctx, p)
		if err != nil {
			return err
		}
		counts, err := r.importPage(ctx, page, keep)
		r.counts.Imported += counts.Imported
		r.counts.Skipped += counts.Skipped
		r.counts.Failed += counts.Failed
		if err != nil {
			return err
		}
		if len(page) < r.s.pageSize {
			return nil
		}
		p.Start += len(page)
	}
}

// importPage downloads and imports the activities in page that haven't been
// handled before and that keep (if not nil) accepts by start time, and
// records what came of each. It stops at the first error that isn't about
// one activity alone, returning the counts so far.
func (r *syncRun) importPage(ctx context.Context, page []garmin.ActivitySummary, keep func(time.Time) bool) (store.BackfillProgress, error) {
	var counts store.BackfillProgress
	ids := make([]string, len(page))
	for i, a := range page {
		ids[i] = strconv.FormatInt(a.ActivityID, 10)
	}
	handled, err := r.s.store.HandledProviderActivities(ctx, r.conn.ID, ids)
	if err != nil {
		return counts, err
	}

	for i, a := range page {
		id := ids[i]
		if handled[id] {
			continue
		}
		started, err := a.StartedAt()
		var startedAt *time.Time
		if err == nil {
			startedAt = &started
		}
		if keep != nil && (startedAt == nil || !keep(started)) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return counts, err
		}
		if time.Now().After(r.deadline) {
			return counts, errOutOfTime
		}

		data, err := withToken(ctx, r, func(token string) ([]byte, error) {
			return r.api.DownloadFIT(ctx, token, a.ActivityID)
		})
		var se *garmin.StatusError
		if errors.As(err, &se) && se.Code == http.StatusNotFound {
			// Deleted since the search, or no original file.
			counts.Failed++
			r.record(ctx, store.ProviderActivity{ExternalID: id, StartedAt: startedAt, Status: string(importer.StatusFailed),
				Error: "Garmin has no file for this activity"})
			continue
		}
		if err != nil {
			return counts, err
		}

		res, err := r.s.importer.Import(ctx, r.conn.UserID, id+".fit", data, importer.Options{
			Source:     ProviderGarmin,
			ExternalID: id,
			Name:       strings.TrimSpace(a.Name),
		})
		if err != nil {
			// The database failed; try this activity again next time.
			return counts, err
		}
		switch res.Status {
		case importer.StatusImported:
			counts.Imported++
		case importer.StatusFailed:
			counts.Failed++
			r.log.Warn("garmin activity could not be imported", "activity_id", id, "error", res.Error)
		default:
			counts.Skipped++
		}
		r.log.Debug("garmin activity handled", "activity_id", id, "status", res.Status)
		r.record(ctx, store.ProviderActivity{ExternalID: id, StartedAt: startedAt, Status: string(res.Status),
			ActivityID: res.ActivityID, Error: res.Error})
	}
	return counts, nil
}

// record records what came of an activity. If that fails the activity is
// handled again next time, which the importer recognizes, so only log it.
func (r *syncRun) record(ctx context.Context, a store.ProviderActivity) {
	if err := r.s.store.RecordProviderActivity(ctx, r.conn.ID, a); err != nil {
		r.log.Error("record handled activity", "activity_id", a.ExternalID, "err", err)
	}
}
