package middleware

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

// LimitConcurrent rejects work when all dedicated slots are occupied. It is
// intended for long-lived endpoints such as SSE so they cannot consume the
// concurrency budget used by short-lived API requests.
func LimitConcurrent(max int) func(http.Handler) http.Handler {
	return LimitConcurrentFor(max, 0)
}

// LimitSSEConcurrentFor isolates long-lived SSE capacity by role and by user.
// A viewer can exhaust only the viewer tier and their own small allowance;
// operator/admin observability remains available during viewer saturation.
func LimitSSEConcurrentFor(viewerMax, operatorMax, adminMax, perUserMax int, maxDuration time.Duration) func(http.Handler) http.Handler {
	if viewerMax < 1 || operatorMax < 1 || adminMax < 1 || perUserMax < 1 {
		panic("SSE concurrency limits must be positive")
	}
	tiers := map[string]chan struct{}{
		shared.RoleViewer:   make(chan struct{}, viewerMax),
		shared.RoleOperator: make(chan struct{}, operatorMax),
		shared.RoleAdmin:    make(chan struct{}, adminMax),
	}
	var users sync.Map

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := UserRoleFromContext(r.Context())
			userID := UserIDFromContext(r.Context())
			tier, ok := tiers[role]
			if !ok || userID == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			userKey := role + ":" + userID
			userValue, _ := users.LoadOrStore(userKey, make(chan struct{}, perUserMax))
			userSlots := userValue.(chan struct{})

			select {
			case tier <- struct{}{}:
				defer func() { <-tier }()
			default:
				rejectConcurrent(w)
				return
			}
			select {
			case userSlots <- struct{}{}:
				defer func() { <-userSlots }()
			default:
				rejectConcurrent(w)
				return
			}
			if maxDuration > 0 {
				ctx, cancel := context.WithTimeout(r.Context(), maxDuration)
				defer cancel()
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func rejectConcurrent(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "5")
	http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
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
				rejectConcurrent(w)
			}
		})
	}
}
