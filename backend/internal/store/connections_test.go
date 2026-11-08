package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testConnection(t *testing.T, s *Store, userID string) Connection {
	t.Helper()
	c, err := s.SaveConnection(context.Background(), userID, "garmin", Credentials{
		APIKeyEnc:            []byte("sealed key"),
		AccessTokenEnc:       []byte("sealed token"),
		AccessTokenExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConnectionsBelongToTheirUser(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	alice := testUser(t, s)
	bob := testUser(t, s)
	ac := testConnection(t, s, alice.ID)

	if conns, err := s.ConnectionsForUser(ctx, bob.ID); err != nil || len(conns) != 0 {
		t.Errorf("bob's connections = %v, %v; want none", conns, err)
	}
	if _, err := s.ConnectionForUser(ctx, bob.ID, "garmin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob's garmin connection: err = %v, want ErrNotFound", err)
	}
	if _, err := s.StartBackfill(ctx, bob.ID, "garmin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob starting a backfill: err = %v, want ErrNotFound", err)
	}
	if deleted, err := s.DeleteConnection(ctx, bob.ID, "garmin"); err != nil || deleted {
		t.Errorf("bob deleting: %v, %v; want nothing deleted", deleted, err)
	}

	got, err := s.ConnectionForUser(ctx, alice.ID, "garmin")
	if err != nil || got.ID != ac.ID || string(got.APIKeyEnc) != "sealed key" {
		t.Errorf("alice's connection = %+v, %v", got, err)
	}
	if deleted, err := s.DeleteConnection(ctx, alice.ID, "garmin"); err != nil || !deleted {
		t.Errorf("alice deleting: %v, %v; want deleted", deleted, err)
	}
}

func TestSaveConnectionReplacesKeyAndKeepsBackfill(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	u := testUser(t, s)
	c := testConnection(t, s, u.ID)
	if _, err := s.StartBackfill(ctx, u.ID, "garmin"); err != nil {
		t.Fatal(err)
	}
	msg := "Garmin rejected the key"
	if err := s.FinishSync(ctx, c.ID, SyncResult{Status: ConnectionInvalidKey, LastError: &msg, NextSyncAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	got, err := s.SaveConnection(ctx, u.ID, "garmin", Credentials{APIKeyEnc: []byte("new key"), AccessTokenEnc: []byte("t"), AccessTokenExpiresAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != c.ID || string(got.APIKeyEnc) != "new key" || got.Status != ConnectionActive || got.LastError != nil {
		t.Errorf("after replacing the key: %+v", got)
	}
	if got.BackfillStatus != BackfillRunning {
		t.Errorf("BackfillStatus = %q, want it still running", got.BackfillStatus)
	}
}

func TestClaimDueConnection(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	u := testUser(t, s)
	c := testConnection(t, s, u.ID)

	// Other connections in the database may be due too; claim until ours
	// comes up. The lease is short so that real connections in a
	// development database are hardly held up.
	const lease = 2 * time.Second
	var claimed Connection
	for range 50 {
		got, err := s.ClaimDueConnection(ctx, lease)
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if got.ID == c.ID {
			claimed = got
			break
		}
	}
	if claimed.ID != c.ID {
		t.Fatal("the new connection was not claimed")
	}
	if !claimed.NextSyncAt.After(time.Now()) {
		t.Errorf("NextSyncAt = %v, want it moved on by the lease", claimed.NextSyncAt)
	}

	// Connections with a rejected key are never claimed.
	if err := s.FinishSync(ctx, c.ID, SyncResult{Status: ConnectionInvalidKey, NextSyncAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		got, err := s.ClaimDueConnection(ctx, lease)
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if got.ID == c.ID {
			t.Fatal("a connection with an invalid key was claimed")
		}
	}
}

func TestBackfillProgressStopsWhenCancelled(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	u := testUser(t, s)
	c := testConnection(t, s, u.ID)
	if _, err := s.StartBackfill(ctx, u.ID, "garmin"); err != nil {
		t.Fatal(err)
	}

	ok, err := s.SaveBackfillProgress(ctx, c.ID, BackfillProgress{Cursor: 99, Imported: 90, Skipped: 8, Failed: 1})
	if err != nil || !ok {
		t.Fatalf("SaveBackfillProgress = %v, %v", ok, err)
	}
	got, err := s.CancelBackfill(ctx, u.ID, "garmin")
	if err != nil || got.BackfillStatus != BackfillCancelled || got.BackfillCursor != 99 || got.BackfillImported != 90 {
		t.Fatalf("after cancelling: %+v, %v", got, err)
	}
	if ok, err := s.SaveBackfillProgress(ctx, c.ID, BackfillProgress{Cursor: 198, Imported: 99}); err != nil || ok {
		t.Errorf("SaveBackfillProgress after cancel = %v, %v; want false", ok, err)
	}
	if err := s.FinishBackfill(ctx, c.ID, BackfillDone); err != nil {
		t.Fatal(err)
	}
	if running, err := s.BackfillRunning(ctx, c.ID); err != nil || running {
		t.Errorf("BackfillRunning = %v, %v", running, err)
	}

	// Starting again begins from the start with fresh counts.
	got, err = s.StartBackfill(ctx, u.ID, "garmin")
	if err != nil || got.BackfillStatus != BackfillRunning || got.BackfillCursor != 0 || got.BackfillImported != 0 {
		t.Errorf("after starting again: %+v, %v", got, err)
	}
}

func TestProviderActivities(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	u := testUser(t, s)
	c := testConnection(t, s, u.ID)

	if latest, err := s.LatestProviderActivityStart(ctx, c.ID); err != nil || latest != nil {
		t.Errorf("latest with none handled = %v, %v", latest, err)
	}
	ins := insert(t, s, u.ID, newActivity(0, 3600, 1), nil)
	t1, t2 := baseStart, baseStart.Add(24*time.Hour)
	for _, a := range []ProviderActivity{
		{ExternalID: "1", StartedAt: &t1, Status: "imported", ActivityID: ins.New.ID},
		{ExternalID: "2", StartedAt: &t2, Status: "possible_duplicate"},
		{ExternalID: "2", StartedAt: &t2, Status: "possible_duplicate"}, // recorded twice
	} {
		if err := s.RecordProviderActivity(ctx, c.ID, a); err != nil {
			t.Fatal(err)
		}
	}
	handled, err := s.HandledProviderActivities(ctx, c.ID, []string{"1", "2", "3"})
	if err != nil || !handled["1"] || !handled["2"] || handled["3"] {
		t.Errorf("handled = %v, %v; want 1 and 2", handled, err)
	}
	if latest, err := s.LatestProviderActivityStart(ctx, c.ID); err != nil || latest == nil || !latest.Equal(t2) {
		t.Errorf("latest = %v, %v; want %v", latest, err, t2)
	}

	// Deleting the activity keeps the record, so it isn't imported again.
	if _, err := s.DeleteActivity(ctx, u.ID, ins.New.ID); err != nil {
		t.Fatal(err)
	}
	if handled, err := s.HandledProviderActivities(ctx, c.ID, []string{"1"}); err != nil || !handled["1"] {
		t.Errorf("after deleting the activity: handled = %v, %v", handled, err)
	}
}
