package web

import (
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
)

// SecurityHeaders adds security response headers to all responses.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
		w.Header().Set("X-XSS-Protection", "0") // Disabled per modern best practice
		next.ServeHTTP(w, r)
	})
}

// RecoverPanic recovers from panics in HTTP handlers, logs the stack trace,
// and returns a 500 response instead of crashing the process.
func RecoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger.Errorw("Panic recovered in HTTP handler",
					"error", err,
					"path", r.URL.Path,
					"method", r.Method,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// MaxBodySize limits the request body to the given number of bytes.
func MaxBodySize(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitByIP returns middleware that limits requests per IP address using
// a fixed-window counter. Stale entries are cleaned up lazily during requests.
func RateLimitByIP(requestsPerMinute int) func(http.Handler) http.Handler {
	type entry struct {
		mu      sync.Mutex
		count   int
		resetAt time.Time
	}

	var clients sync.Map
	var cleanupMu sync.Mutex
	lastCleanup := time.Now()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			now := time.Now()

			// Lazy cleanup: purge stale entries every 2 minutes
			cleanupMu.Lock()
			if now.Sub(lastCleanup) > 2*time.Minute {
				lastCleanup = now
				clients.Range(func(key, value any) bool {
					e := value.(*entry)
					e.mu.Lock()
					stale := now.After(e.resetAt)
					e.mu.Unlock()
					if stale {
						clients.Delete(key)
					}
					return true
				})
			}
			cleanupMu.Unlock()

			val, _ := clients.LoadOrStore(ip, &entry{resetAt: now.Add(time.Minute)})
			e := val.(*entry)

			e.mu.Lock()
			if now.After(e.resetAt) {
				e.count = 0
				e.resetAt = now.Add(time.Minute)
			}
			e.count++
			count := e.count
			e.mu.Unlock()

			if count > requestsPerMinute {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "Too many requests", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdmin is middleware that checks for the admin role in the request context.
// Must be used after session authentication middleware.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, _ := r.Context().Value(middleware.ContextKeyUserRole).(string)
		if role != "admin" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AgentOpsAuth accepts either a browser session or an API key. Agent Ops has
// browser-facing views and producer-facing ingestion endpoints under the same
// /api/agent-ops prefix, so this middleware picks the credential family from
// the request headers before falling back to session auth.
func AgentOpsAuth(sessionAuth *middleware.SessionAuth, apiKeyAuth *middleware.APIKeyMiddleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hasAPIKeyCredential(r) {
				if apiKeyAuth == nil {
					http.Error(w, "API key authentication is unavailable", http.StatusUnauthorized)
					return
				}
				apiKeyAuth.Authenticate(next).ServeHTTP(w, r)
				return
			}

			if sessionAuth == nil {
				http.Error(w, "Session authentication is unavailable", http.StatusUnauthorized)
				return
			}
			sessionAuth.RequireSession(next).ServeHTTP(w, r)
		})
	}
}

func hasAPIKeyCredential(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("X-API-Key")) != "" {
		return true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	return strings.HasPrefix(strings.ToLower(auth), "bearer ")
}

// clientIP extracts the client IP from the request, checking proxy headers.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-Ip"); xri != "" {
		return xri
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}
