package importer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activity"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

const validGPX = `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg>
	<trkpt lat="59.3" lon="18.0"/><trkpt lat="59.4" lon="18.1"/>
</trkseg></trk></gpx>`

type failingStore struct{}

func (failingStore) InsertActivity(context.Context, string, store.NewActivity, *activity.DuplicateRule) (store.InsertResult, error) {
	return store.InsertResult{}, errors.New("connection refused")
}

func TestImportGPXStoreError(t *testing.T) {
	res, err := New(failingStore{}).ImportGPX(context.Background(), "user", "dir/run.gpx", []byte(validGPX), Options{})
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("err = %v, want the store's error for the log", err)
	}
	if res.File != "dir/run.gpx" || res.Status != StatusFailed || res.Error != storeFailedMessage {
		t.Errorf("got %+v, want a failed result for the file with storeFailedMessage", res)
	}
}
