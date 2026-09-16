package observability

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimiter is a bounded process-wide fixed-window limiter. A global
// limiter is intentional for the MVP: it cannot be exhausted by creating an
// unbounded map of attacker-controlled IP or token labels.
type RateLimiter struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	windowStart time.Time
	count       int
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit < 1 || window <= 0 {
		return nil
	}
	return &RateLimiter{limit: limit, window: window}
}

func (limiter *RateLimiter) Allow(now time.Time) (bool, time.Duration) {
	if limiter == nil {
		return true, 0
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.windowStart.IsZero() || now.Before(limiter.windowStart) || !now.Before(limiter.windowStart.Add(limiter.window)) {
		limiter.windowStart = now
		limiter.count = 0
	}
	if limiter.count >= limiter.limit {
		return false, limiter.windowStart.Add(limiter.window).Sub(now)
	}
	limiter.count++
	return true, limiter.windowStart.Add(limiter.window).Sub(now)
}

// RateLimitMiddleware returns a safe 429 response after the process-wide
// budget is exhausted. The response has no client identity or request data.
func RateLimitMiddleware(limiter *RateLimiter, next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		allowed, retryAfter := limiter.Allow(time.Now())
		if !allowed {
			seconds := int(math.Ceil(retryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			writer.Header().Set("Retry-After", strconv.Itoa(seconds))
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"code": "RATE_LIMITED", "message": "Request rate limit exceeded."}})
			return
		}
		next.ServeHTTP(writer, request)
	})
}
