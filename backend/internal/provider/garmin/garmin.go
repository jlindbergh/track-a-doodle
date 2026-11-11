// Package garmin is a client for Garmin's Personal API, which lets a Garmin
// Connect user read their own activities with a personal API key created at
// https://connect.garmin.com/app/settings/personalApiToken.
//
// The key is exchanged for an access token (a JWT valid for about 24 hours;
// there are no refresh tokens), which is then sent with every data request.
// See docs/garmin.md for what has been checked against the real API.
//
// Garmin doesn't document rate limits, so a Client spaces its requests out,
// retries throttled and failed requests with exponential backoff, and logs
// every failed attempt as it happens, to show when and how often Garmin
// pushes back. Keys and tokens are never logged.
package garmin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// DefaultBaseURL is the Personal API's address.
const DefaultBaseURL = "https://personal.connectapi.garmin.com"

const (
	tokenPath    = "/personalapitoken-service/personal-api/personal-api-key/token"
	searchPath   = "/activitylist-service/activities/search/activities"
	downloadPath = "/download-service/files/activity/"

	// maxJSONSize caps the response to a token or search request.
	maxJSONSize = 10 << 20
	// MaxFileSize caps a downloaded activity file, zipped and unzipped. It
	// matches importer.MaxFileSize.
	MaxFileSize = 50 << 20

	// tokenFallbackLifetime is used when a token's expiry cannot be read.
	tokenFallbackLifetime = 12 * time.Hour
)

// accept is the Accept header of every request. It accepts any type on
// purpose. Garmin negotiates on Accept and answers 406 Not Acceptable when
// the header leaves out the type an endpoint responds with: an earlier
// "application/json, application/octet-stream, application/zip" made every
// token request fail, since the token is sent as text/plain. The response
// types aren't documented, no endpoint is known to offer a choice of
// formats, and every response is checked by its content (a JWT, JSON, a zip
// or FIT file), so naming types only risks another 406. It is the header
// curl sends with the calls in Garmin's docs, which is what has been tried
// against the real API; sending no header would mean the same.
const accept = "*/*"

// ErrInvalidKey is returned by Token when Garmin rejects the API key: it is
// wrong, expired or has been deleted.
var ErrInvalidKey = errors.New("garmin: the API key was rejected")

// ErrUnauthorized is returned by data requests when Garmin rejects the access
// token. Make a new one with Token; if that fails too, the key is no longer
// valid.
var ErrUnauthorized = errors.New("garmin: the access token was rejected")

// errTooLarge means a response was larger than the request allowed. It isn't
// retried.
var errTooLarge = errors.New("response too large")

// StatusError is an unexpected HTTP status from Garmin.
type StatusError struct {
	Method, Path string
	Code         int
	// RetryAfter is how long Garmin asked us to wait, if it did.
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("garmin: %s %s: %d %s", e.Method, e.Path, e.Code, http.StatusText(e.Code))
}

// Throttled reports whether Garmin turned the request down because of too
// many requests.
func (e *StatusError) Throttled() bool { return e.Code == http.StatusTooManyRequests }

// Config configures a Client. Zero values get the defaults.
type Config struct {
	// BaseURL defaults to DefaultBaseURL.
	BaseURL string
	// HTTPClient defaults to a client with a 2 minute timeout.
	HTTPClient *http.Client
	// Logger gets the request logs; add the connection and user ids to it
	// with Logger.With. Defaults to slog.Default().
	Logger *slog.Logger
	// MinInterval is the least time between two requests. Defaults to 2s.
	MinInterval time.Duration
	// MaxAttempts is how many times a request is tried. Defaults to 5.
	MaxAttempts int
	// BaseBackoff is the wait after the first failed attempt; it doubles
	// with every attempt, up to MaxBackoff. Default 2s and 2 minutes.
	BaseBackoff, MaxBackoff time.Duration
}

// Client talks to the Personal API on behalf of one connection. It is safe
// for concurrent use, but requests are spaced out by Config.MinInterval.
type Client struct {
	baseURL                 string
	http                    *http.Client
	log                     *slog.Logger
	limiter                 *rate.Limiter
	maxAttempts             int
	baseBackoff, maxBackoff time.Duration
}

// New returns a Client configured by cfg.
func New(cfg Config) *Client {
	c := &Client{
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		http:        cfg.HTTPClient,
		log:         cfg.Logger,
		maxAttempts: cfg.MaxAttempts,
		baseBackoff: cfg.BaseBackoff,
		maxBackoff:  cfg.MaxBackoff,
	}
	if c.baseURL == "" {
		c.baseURL = DefaultBaseURL
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 2 * time.Minute}
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	if c.maxAttempts <= 0 {
		c.maxAttempts = 5
	}
	if c.baseBackoff <= 0 {
		c.baseBackoff = 2 * time.Second
	}
	if c.maxBackoff <= 0 {
		c.maxBackoff = 2 * time.Minute
	}
	interval := cfg.MinInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	c.limiter = rate.NewLimiter(rate.Every(interval), 1)
	return c
}

// Token is an access token for the data requests.
type Token struct {
	Value     string
	ExpiresAt time.Time
}

// Token exchanges a personal API key for an access token. It returns
// ErrInvalidKey if Garmin rejects the key.
func (c *Client) Token(ctx context.Context, apiKey string) (Token, error) {
	body, err := c.do(ctx, http.MethodPost, tokenPath, nil, "ApiKey "+apiKey, maxJSONSize)
	var se *StatusError
	if errors.As(err, &se) && (se.Code == http.StatusUnauthorized || se.Code == http.StatusForbidden) {
		return Token{}, ErrInvalidKey
	}
	if err != nil {
		return Token{}, err
	}
	// The response is the raw JWT; tolerate quotes and whitespace around it.
	value := strings.Trim(strings.TrimSpace(string(body)), `"`)
	if value == "" {
		return Token{}, errors.New("garmin: empty token response")
	}
	exp, err := jwtExpiry(value)
	if err != nil {
		c.log.Warn("garmin token expiry unknown, assuming a short lifetime", "err", err, "lifetime", tokenFallbackLifetime)
		exp = time.Now().Add(tokenFallbackLifetime)
	}
	return Token{Value: value, ExpiresAt: exp}, nil
}

// jwtExpiry reads the exp claim of a JWT, without checking its signature:
// the token is only passed back to Garmin, which checks it.
func jwtExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, errors.New("token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("parse JWT payload: %w", err)
	}
	if claims.Exp <= 0 {
		return time.Time{}, errors.New("JWT has no exp claim")
	}
	return time.Unix(int64(claims.Exp), 0), nil
}

// Sort orders for SearchParams.SortOrder.
const (
	SortAscending  = "asc"
	SortDescending = "desc"
)

// SearchParams filters and pages an activity search. Zero values are left
// out of the request, so Garmin's defaults apply: newest first, 99 at a time.
type SearchParams struct {
	// Start is the offset of the first activity to return.
	Start int
	// Limit is how many activities to return at most.
	Limit int
	// StartDate and EndDate limit the activities' local start dates,
	// inclusive, as YYYY-MM-DD.
	StartDate, EndDate string
	// SortOrder is SortAscending or SortDescending, by local start time.
	SortOrder string
	// ActivityType is a parent type key such as "running".
	ActivityType string
	// ExcludedSubTypes are activity type keys to leave out, e.g.
	// "treadmill_running".
	ExcludedSubTypes []string
	// RequiredFields are summary fields that must not be null. "startLatitude"
	// leaves out activities without GPS.
	RequiredFields []string
}

func (p SearchParams) query() url.Values {
	q := url.Values{}
	if p.Start > 0 {
		q.Set("start", strconv.Itoa(p.Start))
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("startDate", p.StartDate)
	set("endDate", p.EndDate)
	set("sortOrder", p.SortOrder)
	set("activityType", p.ActivityType)
	for _, v := range p.ExcludedSubTypes {
		q.Add("excludedActivitySubType", v)
	}
	for _, v := range p.RequiredFields {
		q.Add("requiredField", v)
	}
	return q
}

// ActivitySummary is one activity in a search result.
type ActivitySummary struct {
	ActivityID int64  `json:"activityId"`
	Name       string `json:"activityName"`
	// StartTimeGMT and StartTimeLocal are "2006-01-02 15:04:05"; use StartedAt.
	StartTimeGMT   string       `json:"startTimeGMT"`
	StartTimeLocal string       `json:"startTimeLocal"`
	ActivityType   ActivityType `json:"activityType"`
	// Distance is in metres, Duration in seconds.
	Distance       float64  `json:"distance"`
	Duration       float64  `json:"duration"`
	HasPolyline    bool     `json:"hasPolyline"`
	StartLatitude  *float64 `json:"startLatitude"`
	StartLongitude *float64 `json:"startLongitude"`
}

// ActivityType is the type of an activity, e.g. "trail_running" with the
// parent type of running.
type ActivityType struct {
	TypeKey      string `json:"typeKey"`
	ParentTypeID int    `json:"parentTypeId"`
}

// garminTimeLayout is how the Personal API writes times.
const garminTimeLayout = time.DateTime

// StartedAt returns when the activity started, in UTC.
func (a ActivitySummary) StartedAt() (time.Time, error) {
	return time.ParseInLocation(garminTimeLayout, a.StartTimeGMT, time.UTC)
}

// SearchActivities returns one page of the user's activities. An empty
// result means there are no more.
func (c *Client) SearchActivities(ctx context.Context, token string, p SearchParams) ([]ActivitySummary, error) {
	body, err := c.do(ctx, http.MethodGet, searchPath, p.query(), "Bearer "+token, maxJSONSize)
	if err != nil {
		return nil, unauthorized(err)
	}
	var out []ActivitySummary
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("garmin: parse activity search: %w", err)
	}
	return out, nil
}

// DownloadError means an activity's download holds no file that can be
// imported. It is about that one activity alone: the others can still be
// synced. It describes the download without holding any of its data.
type DownloadError struct {
	// Reason says what is wrong, e.g. "has no .fit or .gpx file".
	Reason string
	// Size is the download's size in bytes, if known.
	Size int
	// Entries lists the files in the download, if it is a zip.
	Entries []ZipEntry
	// Head is the start of the download, quoted, if it isn't a zip. It
	// tells an error page from an XML file without logging any track data.
	Head string
	// Err is the underlying error, if any.
	Err error
}

// ZipEntry is a file in a downloaded zip.
type ZipEntry struct {
	Name string
	// Size is the uncompressed size the zip states.
	Size uint64
}

func (e ZipEntry) String() string { return fmt.Sprintf("%s (%d bytes)", e.Name, e.Size) }

func (e *DownloadError) Error() string {
	var details []string
	if e.Size > 0 {
		details = append(details, fmt.Sprintf("%d bytes", e.Size))
	}
	if len(e.Entries) > 0 {
		names := make([]string, len(e.Entries))
		for i, f := range e.Entries {
			names[i] = f.String()
		}
		details = append(details, "zip holds "+strings.Join(names, ", "))
	}
	if e.Head != "" {
		details = append(details, "starts with "+e.Head)
	}
	msg := "garmin: the download " + e.Reason
	if len(details) > 0 {
		msg += " (" + strings.Join(details, "; ") + ")"
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *DownloadError) Unwrap() error { return e.Err }

// FileNames returns the names of the files in the download, if it is a zip.
func (e *DownloadError) FileNames() []string {
	names := make([]string, len(e.Entries))
	for i, f := range e.Entries {
		names[i] = f.Name
	}
	return names
}

// ActivityFile is an activity's file, ready for the importer.
type ActivityFile struct {
	// Name is "<activity id>.fit" or "<activity id>.gpx"; the importer
	// picks its parser by the extension.
	Name string
	Data []byte
}

// importableExts are the file types DownloadActivityFile takes from a zip,
// in order of preference: FIT has the most detail.
var importableExts = []string{".fit", ".gpx"}

// DownloadActivityFile downloads an activity's original file, which Garmin
// sends zipped, and returns the FIT or GPX file in it. The original file is
// whatever reached Garmin: FIT from Garmin devices, GPX or TCX from uploads.
// It returns a *DownloadError if the download holds neither FIT nor GPX.
func (c *Client) DownloadActivityFile(ctx context.Context, token string, activityID int64) (ActivityFile, error) {
	id := strconv.FormatInt(activityID, 10)
	body, err := c.do(ctx, http.MethodGet, downloadPath+id, nil, "Bearer "+token, MaxFileSize)
	if errors.Is(err, errTooLarge) {
		return ActivityFile{}, &DownloadError{Reason: fmt.Sprintf("is larger than %d bytes", MaxFileSize)}
	}
	if err != nil {
		return ActivityFile{}, unauthorized(err)
	}
	data, ext, err := fileFromDownload(body)
	if err != nil {
		return ActivityFile{}, err
	}
	return ActivityFile{Name: id + ext, Data: data}, nil
}

// headSize is how much of a download that isn't a zip DownloadError shows.
const headSize = 16

// fileFromDownload returns data itself if it is a FIT file, or else the
// first file in the zip archive data whose type is in importableExts, along
// with that type's extension. It returns a *DownloadError otherwise.
func fileFromDownload(data []byte) ([]byte, string, error) {
	if isFIT(data) {
		return data, ".fit", nil
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		head := data[:min(len(data), headSize)]
		return nil, "", &DownloadError{Reason: "is neither a zip nor a FIT file", Size: len(data), Head: fmt.Sprintf("%q", head), Err: err}
	}
	entries := make([]ZipEntry, len(zr.File))
	for i, f := range zr.File {
		entries[i] = ZipEntry{Name: f.Name, Size: f.UncompressedSize64}
	}
	fail := func(reason string, err error) error {
		return &DownloadError{Reason: reason, Size: len(data), Entries: entries, Err: err}
	}
	for _, ext := range importableExts {
		i := slices.IndexFunc(zr.File, func(f *zip.File) bool { return strings.EqualFold(path.Ext(f.Name), ext) })
		if i < 0 {
			continue
		}
		f := zr.File[i]
		if f.UncompressedSize64 > MaxFileSize {
			return nil, "", fail("has a "+ext+" file that is too large", nil)
		}
		r, err := f.Open()
		if err != nil {
			return nil, "", fail("could not be unzipped", err)
		}
		defer r.Close()
		// The size in the zip header could be wrong; don't trust it.
		file, err := io.ReadAll(io.LimitReader(r, MaxFileSize+1))
		if err != nil {
			return nil, "", fail("could not be unzipped", err)
		}
		if len(file) > MaxFileSize {
			return nil, "", fail("has a "+ext+" file that is too large", nil)
		}
		return file, ext, nil
	}
	return nil, "", fail("has no .fit or .gpx file", nil)
}

// isFIT reports whether data starts with a FIT file header.
func isFIT(data []byte) bool {
	return len(data) >= 12 && (data[0] == 12 || data[0] == 14) && string(data[8:12]) == ".FIT"
}

// unauthorized turns a 401 into ErrUnauthorized.
func unauthorized(err error) error {
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusUnauthorized {
		return fmt.Errorf("%w (%s %s)", ErrUnauthorized, se.Method, se.Path)
	}
	return err
}

// do sends a request with the given Authorization header and returns the
// body of a 2xx response, read up to maxSize bytes. Throttling (429), server
// errors (5xx) and network errors are retried with backoff; other statuses
// are returned at once as a *StatusError. Every failed attempt is logged.
func (c *Client) do(ctx context.Context, method, p string, query url.Values, authorization string, maxSize int64) ([]byte, error) {
	u := c.baseURL + p
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	// Logged instead of u: never anything secret, and short.
	logAttrs := []any{"method", method, "path", p, "query", query.Encode()}

	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		started := time.Now()
		body, err := c.send(ctx, method, u, authorization, maxSize)
		if err == nil {
			c.log.Debug("garmin request", append(logAttrs, "attempt", attempt, "bytes", len(body), "duration", time.Since(started))...)
			return body, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var se *StatusError
		if errors.As(err, &se) {
			se.Method, se.Path = method, p
		}
		// A response too large now is too large next time too.
		retryable := (se == nil && !errors.Is(err, errTooLarge)) || (se != nil && (se.Code == http.StatusTooManyRequests || se.Code >= 500))
		attrs := append(logAttrs, "attempt", attempt, "max_attempts", c.maxAttempts, "duration", time.Since(started))
		if se != nil {
			attrs = append(attrs, "status", se.Code)
			if se.RetryAfter > 0 {
				attrs = append(attrs, "retry_after", se.RetryAfter)
			}
		} else {
			attrs = append(attrs, "err", err)
		}
		lastErr = err
		if !retryable {
			c.log.Warn("garmin request failed", attrs...)
			return nil, err
		}
		if attempt == c.maxAttempts {
			c.log.Error("garmin request failed, giving up", attrs...)
			break
		}
		wait := c.backoff(attempt)
		if se != nil && se.RetryAfter > wait {
			wait = se.RetryAfter
		}
		if wait > c.maxBackoff {
			// Garmin wants us to wait longer than we would block for;
			// let the caller come back later.
			c.log.Error("garmin request failed, retry later", append(attrs, "retry_in", wait)...)
			return nil, err
		}
		c.log.Warn("garmin request failed, retrying", append(attrs, "retry_in", wait)...)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, fmt.Errorf("garmin: %s %s failed %d times: %w", method, p, c.maxAttempts, lastErr)
}

// backoff returns the wait after the given failed attempt: BaseBackoff
// doubled for every earlier attempt, plus up to 25% jitter, at most
// MaxBackoff.
func (c *Client) backoff(attempt int) time.Duration {
	d := c.baseBackoff << (attempt - 1)
	if d <= 0 || d > c.maxBackoff {
		d = c.maxBackoff
	}
	d += time.Duration(rand.Int64N(int64(d)/4 + 1))
	return min(d, c.maxBackoff)
}

// send makes one attempt at a request.
func (c *Client) send(ctx context.Context, method, u, authorization string, maxSize int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "track-a-doodle")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Drain a little so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, &StatusError{Code: resp.StatusCode, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > maxSize {
		return nil, fmt.Errorf("%w: more than %d bytes", errTooLarge, maxSize)
	}
	return body, nil
}

// retryAfter parses a Retry-After header: seconds or an HTTP date.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0)
	}
	return 0
}
