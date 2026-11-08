package garmin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// logRecorder is a slog.Handler that keeps the records it gets.
type logRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *logRecorder) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}
func (h *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *logRecorder) WithGroup(string) slog.Handler      { return h }

// atLevel returns the messages logged at level, with all their attributes.
func (h *logRecorder) atLevel(level slog.Level) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, r := range h.records {
		if r.Level != level {
			continue
		}
		var b strings.Builder
		b.WriteString(r.Message)
		r.Attrs(func(a slog.Attr) bool {
			fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
			return true
		})
		out = append(out, b.String())
	}
	return out
}

// newTestClient returns a Client for srv that doesn't wait between requests.
func newTestClient(srv *httptest.Server) (*Client, *logRecorder) {
	logs := &logRecorder{}
	return New(Config{
		BaseURL:     srv.URL,
		HTTPClient:  srv.Client(),
		Logger:      slog.New(logs),
		MinInterval: time.Microsecond,
		MaxAttempts: 3,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  50 * time.Millisecond,
	}), logs
}

func jwt(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc([]byte(payload)) + ".c2lnbmF0dXJl"
}

func TestToken(t *testing.T) {
	token := jwt(`{"sub":"x","exp":1794200000}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != tokenPath {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "ApiKey secret-key" {
			t.Errorf("Authorization = %q", got)
		}
		fmt.Fprint(w, token+"\n")
	}))
	defer srv.Close()
	c, logs := newTestClient(srv)

	got, err := c.Token(context.Background(), "secret-key")
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got.Value != token || !got.ExpiresAt.Equal(time.Unix(1794200000, 0)) {
		t.Errorf("Token = %+v", got)
	}
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelWarn, slog.LevelError} {
		for _, line := range logs.atLevel(level) {
			if strings.Contains(line, "secret-key") || strings.Contains(line, token) {
				t.Errorf("log line contains a secret: %s", line)
			}
		}
	}
}

func TestTokenWithoutExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not-a-jwt")
	}))
	defer srv.Close()
	c, _ := newTestClient(srv)
	got, err := c.Token(context.Background(), "k")
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if until := time.Until(got.ExpiresAt); until <= 0 || until > tokenFallbackLifetime {
		t.Errorf("ExpiresAt in %v, want within the fallback lifetime", until)
	}
}

func TestTokenInvalidKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c, _ := newTestClient(srv)
	if _, err := c.Token(context.Background(), "bad"); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("err = %v, want ErrInvalidKey", err)
	}
}

func TestSearchActivitiesQueryAndResult(t *testing.T) {
	var gotQuery map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != searchPath || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("got %s with Authorization %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		gotQuery = r.URL.Query()
		fmt.Fprint(w, `[{"activityId":25007758253,"activityName":"Stockholm Running","startTimeLocal":"2026-11-08 11:30:35",
			"startTimeGMT":"2026-11-08 10:30:35","activityType":{"typeId":1,"typeKey":"running","parentTypeId":17},
			"distance":10012.5,"duration":3000.5,"hasPolyline":true,"startLatitude":59.3,"startLongitude":18.0}]`)
	}))
	defer srv.Close()
	c, _ := newTestClient(srv)

	got, err := c.SearchActivities(context.Background(), "tok", SearchParams{
		Start: 99, Limit: 50, SortOrder: SortAscending, StartDate: "2026-01-01",
		RequiredFields:   []string{"startLatitude"},
		ExcludedSubTypes: []string{"treadmill_running", "indoor_cycling"},
	})
	if err != nil {
		t.Fatalf("SearchActivities: %v", err)
	}
	wantQuery := map[string][]string{
		"start": {"99"}, "limit": {"50"}, "sortOrder": {"asc"}, "startDate": {"2026-01-01"},
		"requiredField":           {"startLatitude"},
		"excludedActivitySubType": {"treadmill_running", "indoor_cycling"},
	}
	if !reflect.DeepEqual(gotQuery, wantQuery) {
		t.Errorf("query = %v, want %v", gotQuery, wantQuery)
	}
	if len(got) != 1 {
		t.Fatalf("got %d activities, want 1", len(got))
	}
	a := got[0]
	if a.ActivityID != 25007758253 || a.Name != "Stockholm Running" || a.ActivityType.TypeKey != "running" ||
		!a.HasPolyline || a.StartLatitude == nil || *a.StartLatitude != 59.3 {
		t.Errorf("activity = %+v", a)
	}
	start, err := a.StartedAt()
	if err != nil || !start.Equal(time.Date(2026, 11, 8, 10, 30, 35, 0, time.UTC)) {
		t.Errorf("StartedAt = %v, %v", start, err)
	}
}

func TestSearchActivitiesOmitsZeroParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("query = %q, want none", r.URL.RawQuery)
		}
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()
	c, _ := newTestClient(srv)
	got, err := c.SearchActivities(context.Background(), "tok", SearchParams{})
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want an empty page", got, err)
	}
}

func TestRetriesThrottledRequestAndLogsIt(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()
	c, logs := newTestClient(srv)

	if _, err := c.SearchActivities(context.Background(), "tok", SearchParams{Start: 5}); err != nil {
		t.Fatalf("SearchActivities: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	warns := logs.atLevel(slog.LevelWarn)
	if len(warns) != 1 {
		t.Fatalf("warnings = %q, want one for the throttled attempt", warns)
	}
	for _, want := range []string{"garmin request failed, retrying", "status=429", "attempt=1", "path=" + searchPath, "query=start=5", "retry_in="} {
		if !strings.Contains(warns[0], want) {
			t.Errorf("warning %q lacks %q", warns[0], want)
		}
	}
	if strings.Contains(warns[0], "tok") && strings.Contains(warns[0], "Bearer") {
		t.Errorf("warning contains the token: %s", warns[0])
	}
}

func TestRetryAfterHeader(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c, logs := newTestClient(srv)

	_, err := c.SearchActivities(context.Background(), "tok", SearchParams{})
	var se *StatusError
	if !errors.As(err, &se) || !se.Throttled() || se.RetryAfter != time.Hour {
		t.Fatalf("err = %v, want a throttled StatusError asking for an hour", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1: an hour is longer than the client waits", calls)
	}
	if errs := logs.atLevel(slog.LevelError); len(errs) != 1 || !strings.Contains(errs[0], "retry_after=1h0m0s") {
		t.Errorf("errors = %q, want one with retry_after", errs)
	}
}

func TestGivesUpAfterMaxAttempts(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c, logs := newTestClient(srv)

	_, err := c.SearchActivities(context.Background(), "tok", SearchParams{})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadGateway {
		t.Fatalf("err = %v, want a 502 StatusError", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (MaxAttempts)", calls)
	}
	if w, e := logs.atLevel(slog.LevelWarn), logs.atLevel(slog.LevelError); len(w) != 2 || len(e) != 1 {
		t.Errorf("got %d warnings and %d errors, want 2 and 1", len(w), len(e))
	}
}

func TestDoesNotRetryClientErrors(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c, logs := newTestClient(srv)

	_, err := c.DownloadFIT(context.Background(), "tok", 42)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusNotFound || se.Path != downloadPath+"42" {
		t.Fatalf("err = %v, want a 404 StatusError for the download", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
	if w := logs.atLevel(slog.LevelWarn); len(w) != 1 {
		t.Errorf("warnings = %q, want one", w)
	}
}

func TestUnauthorizedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c, _ := newTestClient(srv)
	if _, err := c.SearchActivities(context.Background(), "old", SearchParams{}); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

// fitHeader is the start of a FIT file: header size 14, ".FIT" at offset 8.
var fitHeader = []byte{14, 0x20, 0, 0, 0, 0, 0, 0, '.', 'F', 'I', 'T', 0, 0, 1, 2, 3}

func zipped(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDownloadFIT(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		want    []byte
		wantErr string
	}{
		{"zipped", zipped(t, map[string][]byte{"25007758253_ACTIVITY.fit": fitHeader}), fitHeader, ""},
		{"upper case extension", zipped(t, map[string][]byte{"A.FIT": fitHeader}), fitHeader, ""},
		{"plain FIT", fitHeader, fitHeader, ""},
		{"no fit in zip", zipped(t, map[string][]byte{"readme.txt": []byte("hi")}), nil, "no .fit file"},
		{"not a zip", []byte("<html>error</html>"), nil, "neither a zip nor a FIT file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != downloadPath+"25007758253" {
					t.Errorf("path = %s", r.URL.Path)
				}
				w.Write(tt.body)
			}))
			defer srv.Close()
			c, _ := newTestClient(srv)
			got, err := c.DownloadFIT(context.Background(), "tok", 25007758253)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !bytes.Equal(got, tt.want) {
				t.Errorf("DownloadFIT = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestDownloadFITTooLarge(t *testing.T) {
	big := bytes.Repeat([]byte{0}, MaxFITSize+1)
	if _, err := fitFromDownload(zipped(t, map[string][]byte{"big.fit": big})); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("err = %v, want too large", err)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	if got := retryAfter("120"); got != 2*time.Minute {
		t.Errorf("retryAfter(120) = %v", got)
	}
	if got := retryAfter(time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)); got < 59*time.Minute || got > time.Hour {
		t.Errorf("retryAfter(date) = %v, want about an hour", got)
	}
	for _, v := range []string{"", "soon", "-5"} {
		if got := retryAfter(v); got != 0 {
			t.Errorf("retryAfter(%q) = %v, want 0", v, got)
		}
	}
}
