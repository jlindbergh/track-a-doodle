// Package ratelimit limits how often a key, such as a client IP, may do
// something. State is kept in memory, so limits are per backend process.
package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Keyed is a token-bucket limiter per key.
type Keyed struct {
	limit rate.Limit
	burst int
	// idle is how long an untouched key is kept. After that its bucket has
	// refilled anyway, so forgetting it changes nothing.
	idle time.Duration

	mu        sync.Mutex
	entries   map[string]*entry
	lastPrune time.Time
	now       func() time.Time
}

type entry struct {
	lim  *rate.Limiter
	seen time.Time
}

// NewKeyed allows each key burst events at once, refilled at n per period.
func NewKeyed(n int, period time.Duration, burst int) *Keyed {
	lim := rate.Limit(float64(n) / period.Seconds())
	return &Keyed{
		limit:   lim,
		burst:   burst,
		idle:    time.Duration(float64(burst)/float64(lim)*float64(time.Second)) + time.Minute,
		entries: map[string]*entry{},
		now:     time.Now,
	}
}

// Allow reports whether key may do one more event now, and counts it if so.
func (k *Keyed) Allow(key string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.now()
	if now.Sub(k.lastPrune) > k.idle {
		for key, e := range k.entries {
			if now.Sub(e.seen) > k.idle {
				delete(k.entries, key)
			}
		}
		k.lastPrune = now
	}
	e, ok := k.entries[key]
	if !ok {
		e = &entry{lim: rate.NewLimiter(k.limit, k.burst)}
		k.entries[key] = e
	}
	e.seen = now
	return e.lim.AllowN(now, 1)
}

// ClientIP returns the IP the request came from. With trustProxy, the
// rightmost address in X-Forwarded-For is used, as added by the one proxy
// in front of the backend; never set it when clients can reach the backend
// directly, since they could then pick their own IP.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			last := xff[len(xff)-1]
			if i := strings.LastIndex(last, ","); i >= 0 {
				last = last[i+1:]
			}
			if ip := net.ParseIP(strings.TrimSpace(last)); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
