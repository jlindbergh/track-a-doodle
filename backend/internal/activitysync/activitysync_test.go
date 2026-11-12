package activitysync

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"

	"github.com/jlindbergh/track-a-doodle/backend/internal/importer"
	"github.com/jlindbergh/track-a-doodle/backend/internal/provider/garmin"
	"github.com/jlindbergh/track-a-doodle/backend/internal/secretbox"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

// These tests need a database, like the store's: set TEST_DATABASE_URL.
// They only ever sync their own test users' connections, never "any due
// connection", so real connections in a development database are left alone.

// fakeGarmin is a Garmin with an account per API key.
type fakeGarmin struct {
	mu sync.Mutex
	// accounts maps API keys to their activities, oldest first.
	accounts map[string][]fakeActivity
	// rejectTokens lists access tokens to answer with 401 once.
	rejectTokens map[string]bool
	// searchErr, if set, is returned by every search.
	searchErr error
	// downloadErrs maps activity ids to the error their download returns.
	downloadErrs map[int64]error
	tokens       int
	downloads    []int64
	searches     []garmin.SearchParams
}

type fakeActivity struct {
	summary garmin.ActivitySummary
	file    garmin.ActivityFile
}

func newFakeGarmin() *fakeGarmin {
	return &fakeGarmin{accounts: map[string][]fakeActivity{}, rejectTokens: map[string]bool{}, downloadErrs: map[int64]error{}}
}

func (f *fakeGarmin) Token(_ context.Context, apiKey string) (garmin.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.accounts[apiKey]; !ok {
		return garmin.Token{}, garmin.ErrInvalidKey
	}
	f.tokens++
	return garmin.Token{Value: "token-" + strconv.Itoa(f.tokens) + "-" + apiKey, ExpiresAt: time.Now().Add(24 * time.Hour)}, nil
}

// account returns the activities of the key that token was made from.
func (f *fakeGarmin) account(token string) ([]fakeActivity, error) {
	if f.rejectTokens[token] {
		delete(f.rejectTokens, token)
		return nil, garmin.ErrUnauthorized
	}
	_, key, ok := strings.Cut(strings.TrimPrefix(token, "token-"), "-")
	acts, known := f.accounts[key]
	if !ok || !known {
		return nil, garmin.ErrUnauthorized
	}
	return acts, nil
}

func (f *fakeGarmin) SearchActivities(_ context.Context, token string, p garmin.SearchParams) ([]garmin.ActivitySummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches = append(f.searches, p)
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	acts, err := f.account(token)
	if err != nil {
		return nil, err
	}
	var out []garmin.ActivitySummary
	for _, a := range acts {
		if p.StartDate != "" && a.summary.StartTimeGMT[:10] < p.StartDate {
			continue
		}
		out = append(out, a.summary)
	}
	if p.SortOrder != garmin.SortAscending {
		slices.Reverse(out)
	}
	if p.Start >= len(out) {
		return []garmin.ActivitySummary{}, nil
	}
	out = out[p.Start:]
	if p.Limit > 0 && len(out) > p.Limit {
		out = out[:p.Limit]
	}
	return out, nil
}

func (f *fakeGarmin) DownloadActivityFile(_ context.Context, token string, id int64) (garmin.ActivityFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	acts, err := f.account(token)
	if err != nil {
		return garmin.ActivityFile{}, err
	}
	f.downloads = append(f.downloads, id)
	if err := f.downloadErrs[id]; err != nil {
		return garmin.ActivityFile{}, err
	}
	for _, a := range acts {
		if a.summary.ActivityID == id {
			return a.file, nil
		}
	}
	return garmin.ActivityFile{}, &garmin.StatusError{Code: 404}
}

// add gives the account with apiKey an activity starting at start, with a
// GPS track whose length depends on lat0.
func (f *fakeGarmin) add(t *testing.T, apiKey string, id int64, name string, start time.Time, lat0 float64) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	lat := lat0
	f.accounts[apiKey] = append(f.accounts[apiKey], fakeActivity{
		summary: garmin.ActivitySummary{
			ActivityID: id, Name: name, StartTimeGMT: start.UTC().Format(time.DateTime),
			ActivityType: garmin.ActivityType{TypeKey: "running"}, HasPolyline: true, StartLatitude: &lat,
		},
		file: garmin.ActivityFile{Name: strconv.FormatInt(id, 10) + ".fit", Data: encodeFIT(t, start, lat0)},
	})
}

func encodeFIT(t *testing.T, start time.Time, lat0 float64) []byte {
	t.Helper()
	fa := filedef.NewActivity()
	fa.FileId = *mesgdef.NewFileId(nil).SetType(typedef.FileActivity).SetTimeCreated(start)
	fa.Sessions = []*mesgdef.Session{mesgdef.NewSession(nil).SetSport(typedef.SportRunning).SetStartTime(start)}
	for i := range 10 {
		fa.Records = append(fa.Records, mesgdef.NewRecord(nil).SetTimestamp(start.Add(time.Duration(i)*time.Minute)).
			SetPositionLatDegrees(lat0+float64(i)*0.001).SetPositionLongDegrees(18.0))
	}
	f := fa.ToFIT(nil)
	var buf bytes.Buffer
	if err := encoder.New(&buf).Encode(&f); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type testEnv struct {
	st     *store.Store
	svc    *Service
	garmin *fakeGarmin
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	st, err := store.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	box, err := secretbox.New(bytes.Repeat([]byte{1}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	fg := newFakeGarmin()
	svc := New(Config{
		Store:     st,
		Importer:  importer.New(st),
		Box:       box,
		Logger:    slog.New(slog.DiscardHandler),
		NewGarmin: func(*slog.Logger) GarminAPI { return fg },
		PageSize:  2,
	})
	return &testEnv{st: st, svc: svc, garmin: fg}
}

func (e *testEnv) user(t *testing.T) string {
	t.Helper()
	u, err := e.st.EnsureUserByEmail(context.Background(), "test-"+rand.Text()+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.st.DeleteUser(context.Background(), u.ID); err != nil {
			t.Errorf("delete test user: %v", err)
		}
	})
	return u.ID
}

// sync syncs userID's connection once, as the worker would after claiming it.
func (e *testEnv) sync(t *testing.T, userID string) store.Connection {
	t.Helper()
	ctx := context.Background()
	c, err := e.st.ConnectionForUser(ctx, userID, ProviderGarmin)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.syncConnection(ctx, c)
	c, err = e.st.ConnectionForUser(ctx, userID, ProviderGarmin)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// syncUntilIdle syncs until the connection isn't due again right away.
func (e *testEnv) syncUntilIdle(t *testing.T, userID string) store.Connection {
	t.Helper()
	for range 20 {
		c := e.sync(t, userID)
		if time.Until(c.NextSyncAt) > time.Minute || c.Status != store.ConnectionActive {
			return c
		}
	}
	t.Fatal("the sync never settled")
	return store.Connection{}
}

// status returns what userID is shown about their Garmin connection.
func (e *testEnv) status(t *testing.T, userID string) Status {
	t.Helper()
	conns, err := e.svc.Connections(context.Background(), userID)
	if err != nil || len(conns) != 1 {
		t.Fatalf("connections = %v, %v; want one", conns, err)
	}
	return conns[0]
}

func (e *testEnv) activities(t *testing.T, userID string) map[string]string {
	t.Helper()
	list, err := e.st.ListActivities(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, a := range list {
		names[a.Name] = a.Sport
	}
	return names
}

var day = time.Date(2025, 3, 1, 7, 0, 0, 0, time.UTC)

func TestConnectRejectsBadKeys(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t)
	if _, err := e.svc.ConnectGarmin(context.Background(), u, "  ", nil); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("empty key: err = %v", err)
	}
	if _, err := e.svc.ConnectGarmin(context.Background(), u, "unknown", nil); !errors.Is(err, ErrKeyRejected) {
		t.Errorf("unknown key: err = %v", err)
	}
	if conns, _ := e.svc.Connections(context.Background(), u); len(conns) != 0 {
		t.Errorf("connections = %v, want none after rejected keys", conns)
	}
}

func TestConnectStoresOnlyEncryptedKey(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t)
	e.garmin.accounts["very-secret-key"] = nil
	if _, err := e.svc.ConnectGarmin(context.Background(), u, " very-secret-key\n", nil); err != nil {
		t.Fatal(err)
	}
	c, err := e.st.ConnectionForUser(context.Background(), u, ProviderGarmin)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(c.APIKeyEnc, []byte("very-secret-key")) || bytes.Contains(c.AccessTokenEnc, []byte("very-secret-key")) {
		t.Error("the key is stored in the clear")
	}
}

func TestBackfillImportsEverythingOnce(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t)
	const key = "key-backfill"
	e.garmin.add(t, key, 1, "First run", day, 59.30)
	e.garmin.add(t, key, 2, "Second run", day.Add(24*time.Hour), 59.31)
	e.garmin.add(t, key, 3, "Third run", day.Add(48*time.Hour), 59.32)
	e.garmin.add(t, key, 4, "Fourth run", day.Add(72*time.Hour), 59.33)
	e.garmin.add(t, key, 5, "Fifth run", day.Add(96*time.Hour), 59.34)

	ctx := context.Background()
	if _, err := e.svc.ConnectGarmin(ctx, u, key, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartBackfill(ctx, u, ProviderGarmin); err != nil {
		t.Fatal(err)
	}
	c := e.syncUntilIdle(t, u)

	if c.BackfillStatus != store.BackfillDone || c.BackfillCursor != 5 || c.Status != store.ConnectionActive {
		t.Errorf("connection after backfill = %+v", c)
	}
	st := e.status(t, u)
	if want := (ActivityCounts{Imported: 5}); st.Activities != want {
		t.Errorf("activities = %+v, want %+v", st.Activities, want)
	}
	if last := day.Add(96 * time.Hour); st.Backfill.ReachedAt == nil || !st.Backfill.ReachedAt.Equal(last) {
		t.Errorf("reached %v, want the last run's start, %v", st.Backfill.ReachedAt, last)
	}
	got := e.activities(t, u)
	if len(got) != 5 || got["Third run"] != "running" {
		t.Errorf("activities = %v, want the five runs, named by Garmin", got)
	}
	for _, p := range e.garmin.searches {
		if !slices.Equal(p.RequiredFields, []string{"startLatitude"}) || p.SortOrder != garmin.SortAscending {
			t.Errorf("search %+v, want GPS activities oldest first", p)
		}
	}

	// Starting the backfill again keeps the counts: they come from what was
	// handled, not from the backfill.
	if st, err := e.svc.StartBackfill(ctx, u, ProviderGarmin); err != nil || st.Activities.Imported != 5 {
		t.Errorf("after starting again: %+v, %v; want the five imported still counted", st, err)
	}
	e.syncUntilIdle(t, u)

	// Polling afterwards downloads nothing again.
	downloads := len(e.garmin.downloads)
	e.sync(t, u)
	if len(e.garmin.downloads) != downloads {
		t.Errorf("downloads went from %d to %d on a poll with nothing new", downloads, len(e.garmin.downloads))
	}

	// A new activity is picked up by the next poll.
	e.garmin.add(t, key, 6, "Sixth run", day.Add(120*time.Hour), 59.35)
	e.sync(t, u)
	if got := e.activities(t, u); got["Sixth run"] == "" {
		t.Errorf("activities = %v, want the sixth run", got)
	}
}

func TestUnusableDownloadFailsOnlyThatActivity(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t)
	const key = "key-unusable"
	e.garmin.add(t, key, 1, "First run", day, 59.30)
	e.garmin.add(t, key, 2, "Uploaded as TCX", day.Add(24*time.Hour), 59.31)
	e.garmin.add(t, key, 3, "Third run", day.Add(48*time.Hour), 59.32)
	e.garmin.downloadErrs[2] = &garmin.DownloadError{Reason: "has no .fit or .gpx file", Entries: []garmin.ZipEntry{{Name: "2.tcx", Size: 100}}}

	ctx := context.Background()
	if _, err := e.svc.ConnectGarmin(ctx, u, key, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartBackfill(ctx, u, ProviderGarmin); err != nil {
		t.Fatal(err)
	}
	c := e.syncUntilIdle(t, u)

	if c.Status != store.ConnectionActive || c.LastError != nil {
		t.Errorf("status = %s, last error = %v; want active without an error", c.Status, c.LastError)
	}
	if st := e.status(t, u); c.BackfillStatus != store.BackfillDone || st.Activities != (ActivityCounts{Imported: 2, Failed: 1}) {
		t.Errorf("backfill = %s, activities = %+v; want done, 2 imported and 1 failed", c.BackfillStatus, st.Activities)
	}
	if got := e.activities(t, u); len(got) != 2 || got["First run"] == "" || got["Third run"] == "" {
		t.Errorf("activities = %v, want the first and third runs", got)
	}
	handled, err := e.st.HandledProviderActivities(ctx, c.ID, []string{"2"})
	if err != nil || !handled["2"] {
		t.Errorf("activity 2 handled = %v (%v), want it recorded", handled["2"], err)
	}

	// The unusable file isn't downloaded again.
	downloads := len(e.garmin.downloads)
	e.sync(t, u)
	if len(e.garmin.downloads) != downloads {
		t.Errorf("downloads went from %d to %d on a poll with nothing new", downloads, len(e.garmin.downloads))
	}
}

func TestBackfillImportsGPXOriginals(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t)
	const key = "key-gpx"
	e.garmin.add(t, key, 1, "First run", day, 59.30)
	e.garmin.add(t, key, 2, "Uploaded as GPX", day.Add(24*time.Hour), 59.31)
	e.garmin.accounts[key][1].file = garmin.ActivityFile{Name: "2.gpx", Data: []byte(`<?xml version="1.0" encoding="UTF-8"?>
<gpx version="1.1" creator="test" xmlns="http://www.topografix.com/GPX/1/1">
  <trk><trkseg>
    <trkpt lat="59.3100" lon="18.0000"><time>2025-03-02T07:00:00Z</time></trkpt>
    <trkpt lat="59.3110" lon="18.0010"><time>2025-03-02T07:05:00Z</time></trkpt>
    <trkpt lat="59.3120" lon="18.0020"><time>2025-03-02T07:10:00Z</time></trkpt>
  </trkseg></trk>
</gpx>`)}

	ctx := context.Background()
	if _, err := e.svc.ConnectGarmin(ctx, u, key, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartBackfill(ctx, u, ProviderGarmin); err != nil {
		t.Fatal(err)
	}
	c := e.syncUntilIdle(t, u)

	if st := e.status(t, u); c.BackfillStatus != store.BackfillDone || st.Activities != (ActivityCounts{Imported: 2}) {
		t.Errorf("backfill = %s, activities = %+v; want done and 2 imported", c.BackfillStatus, st.Activities)
	}
	got := e.activities(t, u)
	if _, ok := got["Uploaded as GPX"]; !ok || len(got) != 2 {
		t.Errorf("activities = %v, want both, the GPX one under Garmin's name", got)
	}
}

func TestUnusableFileMessage(t *testing.T) {
	de := &garmin.DownloadError{Reason: "has no .fit or .gpx file", Entries: []garmin.ZipEntry{{Name: "2.tcx", Size: 100}}}
	want := "Garmin's file for this activity can't be imported: the download has no .fit or .gpx file (it holds 2.tcx)"
	if got := unusableFileMessage(de); got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

func TestBackfillSkipsActivitiesAlreadyImportedAsFiles(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t)
	const key = "key-duplicates"
	e.garmin.add(t, key, 1, "Run", day, 59.30)
	ctx := context.Background()

	// The same activity uploaded earlier as a file.
	if res, err := importer.New(e.st).Import(ctx, u, "run.fit", e.garmin.accounts[key][0].file.Data, importer.Options{}); err != nil || res.Status != importer.StatusImported {
		t.Fatalf("upload: %+v, %v", res, err)
	}
	if _, err := e.svc.ConnectGarmin(ctx, u, key, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartBackfill(ctx, u, ProviderGarmin); err != nil {
		t.Fatal(err)
	}
	e.syncUntilIdle(t, u)
	if st := e.status(t, u); st.Activities != (ActivityCounts{Skipped: 1}) {
		t.Errorf("activities = %+v; want the upload's twin skipped", st.Activities)
	}
	if got := e.activities(t, u); len(got) != 1 {
		t.Errorf("activities = %v, want just the upload", got)
	}
}

func TestPollWithoutBackfillOnlyImportsNewActivities(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t)
	const key = "key-poll"
	e.garmin.add(t, key, 1, "Old run", time.Now().Add(-48*time.Hour), 59.30)
	e.garmin.add(t, key, 2, "Run before connecting", time.Now().Add(-time.Hour), 59.31)
	ctx := context.Background()
	if _, err := e.svc.ConnectGarmin(ctx, u, key, nil); err != nil {
		t.Fatal(err)
	}
	e.garmin.add(t, key, 3, "Run after connecting", time.Now().Add(time.Minute), 59.32)

	c := e.sync(t, u)
	if got := e.activities(t, u); len(got) != 1 || got["Run after connecting"] == "" {
		t.Errorf("activities = %v, want only the run after connecting", got)
	}
	if c.BackfillStatus != store.BackfillNotRequested || time.Until(c.NextSyncAt) < 29*time.Minute {
		t.Errorf("connection = %+v, want no backfill and the next poll in 30 minutes", c)
	}
}

func TestUsersOnlyGetTheirOwnActivities(t *testing.T) {
	e := newTestEnv(t)
	alice, bob := e.user(t), e.user(t)
	e.garmin.add(t, "alice-key", 1, "Alice's run", day, 59.30)
	e.garmin.add(t, "bob-key", 2, "Bob's ride", day, 57.70)
	ctx := context.Background()
	for user, key := range map[string]string{alice: "alice-key", bob: "bob-key"} {
		if _, err := e.svc.ConnectGarmin(ctx, user, key, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.StartBackfill(ctx, user, ProviderGarmin); err != nil {
			t.Fatal(err)
		}
	}
	e.syncUntilIdle(t, alice)
	e.syncUntilIdle(t, bob)

	if got := e.activities(t, alice); len(got) != 1 || got["Alice's run"] == "" {
		t.Errorf("alice's activities = %v", got)
	}
	if got := e.activities(t, bob); len(got) != 1 || got["Bob's ride"] == "" {
		t.Errorf("bob's activities = %v", got)
	}
	if err := e.svc.Disconnect(ctx, bob, ProviderGarmin); err != nil {
		t.Fatal(err)
	}
	if conns, _ := e.svc.Connections(ctx, alice); len(conns) != 1 {
		t.Errorf("bob disconnecting changed alice's connections: %v", conns)
	}
	if err := e.svc.Disconnect(ctx, bob, ProviderGarmin); !errors.Is(err, ErrNotConnected) {
		t.Errorf("disconnecting twice: err = %v", err)
	}
}

func TestRejectedKeyStopsSyncing(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t)
	const key = "key-to-delete"
	e.garmin.add(t, key, 1, "Run", day, 59.30)
	ctx := context.Background()
	if _, err := e.svc.ConnectGarmin(ctx, u, key, nil); err != nil {
		t.Fatal(err)
	}
	// The user deletes the key in Garmin Connect, and the token is rejected.
	delete(e.garmin.accounts, key)
	c := e.sync(t, u)
	if c.Status != store.ConnectionInvalidKey || c.LastError == nil || !strings.Contains(*c.LastError, "rejected") {
		t.Errorf("connection = %+v, want invalid_key with a message", c)
	}
}

func TestExpiredTokenIsRenewed(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t)
	const key = "key-renew"
	e.garmin.add(t, key, 1, "Run", day, 59.30)
	ctx := context.Background()
	if _, err := e.svc.ConnectGarmin(ctx, u, key, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartBackfill(ctx, u, ProviderGarmin); err != nil {
		t.Fatal(err)
	}
	e.garmin.rejectTokens["token-1-"+key] = true

	c := e.syncUntilIdle(t, u)
	if c.Status != store.ConnectionActive || e.status(t, u).Activities.Imported != 1 || e.garmin.tokens != 2 {
		t.Errorf("connection = %+v after %d tokens, want the run imported with a second token", c, e.garmin.tokens)
	}
}

func TestThrottlingPostponesSync(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t)
	const key = "key-throttled"
	e.garmin.add(t, key, 1, "Run", day, 59.30)
	ctx := context.Background()
	if _, err := e.svc.ConnectGarmin(ctx, u, key, nil); err != nil {
		t.Fatal(err)
	}
	e.garmin.searchErr = &garmin.StatusError{Code: 429, RetryAfter: 2 * time.Hour}

	c := e.sync(t, u)
	if c.Status != store.ConnectionError || c.LastError == nil || !strings.Contains(*c.LastError, "limiting") {
		t.Errorf("connection = %+v, want an error about Garmin limiting requests", c)
	}
	if until := time.Until(c.NextSyncAt); until < 119*time.Minute || until > 2*time.Hour {
		t.Errorf("next sync in %v, want Garmin's Retry-After of 2 hours", until)
	}
}

func TestCancelledBackfillStops(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t)
	const key = "key-cancel"
	for i := range 4 {
		e.garmin.add(t, key, int64(i+1), "Run "+strconv.Itoa(i+1), day.Add(time.Duration(i)*24*time.Hour), 59.3+float64(i)/100)
	}
	ctx := context.Background()
	if _, err := e.svc.ConnectGarmin(ctx, u, key, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartBackfill(ctx, u, ProviderGarmin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CancelBackfill(ctx, u, ProviderGarmin); err != nil {
		t.Fatal(err)
	}
	c := e.sync(t, u)
	if c.BackfillStatus != store.BackfillCancelled || len(e.activities(t, u)) != 0 {
		t.Errorf("connection = %+v with %d activities, want a cancelled backfill and nothing imported",
			c, len(e.activities(t, u)))
	}
}
