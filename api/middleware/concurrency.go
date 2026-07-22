package middleware

import (
	"context"
	"net/http"
	"time"
)

// LimitConcurrent rejects work when all dedicated slots are occupied. It is
// intended for long-lived endpoints such as SSE so they cannot consume the
// concurrency budget used by short-lived API requests.
func LimitConcurrent(max int) func(http.Handler) http.Handler {
	return LimitConcurrentFor(max, 0)
}

// LimitConcurrentFor additionally bounds the lifetime of each accepted
// request. A zero duration leaves lifetime control to the client context.
func LimitConcurrentFor(max int, maxDuration time.Duration) func(http.Handler) http.Handler {
	if max < 1 {
		panic("concurrency limit must be positive")
	}
	slots := make(chan struct{}, max)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
				if maxDuration > 0 {
					ctx, cancel := context.WithTimeout(r.Context(), maxDuration)
					defer cancel()
					r = r.WithContext(ctx)
				}
				next.ServeHTTP(w, r)
			default:
				w.Header().Set("Retry-After", "5")
				http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			}
		})
	}
}
