package web

import (
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	runtimeconfig "github.com/Constellation-Overwatch/constellation-overwatch/pkg/config"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
)

// SecurityHeaders adds security response headers to all responses.
func SecurityHeaders(next http.Handler) http.Handler {
	return SecurityHeadersWithConfig(nil)(next)
}

// SecurityHeadersWithConfig binds CSP and HSTS to the validated startup
// snapshot. HSTS is emitted only in production.
func SecurityHeadersWithConfig(runtime *runtimeconfig.Runtime) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
			w.Header().Set("X-XSS-Protection", "0") // Disabled per modern best practice
			if runtime != nil {
				w.Header().Set("X-Overwatch-Environment", runtime.Environment)
				w.Header().Set("Content-Security-Policy", runtime.ContentSecurity)
				if runtime.StrictTransport != "" {
					w.Header().Set("Strict-Transport-Security", runtime.StrictTransport)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
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
	return RateLimitByIPWithConfig(requestsPerMinute, nil)
}

// RateLimitByIPWithConfig trusts forwarding headers only when the direct peer
// matches an explicitly configured proxy CIDR.
func RateLimitByIPWithConfig(requestsPerMinute int, runtime *runtimeconfig.Runtime) func(http.Handler) http.Handler {
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
			ip := clientIP(r, runtime)
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
	return RequireRole("admin")(next)
}

// RequireOperator permits entity and fleet mutations by operators and admins.
// Must be used after session authentication middleware.
func RequireOperator(next http.Handler) http.Handler {
	return RequireRole("operator", "admin")(next)
}

// RequireViewer permits authenticated browser reads for every supported role.
// Must be used after session authentication middleware.
func RequireViewer(next http.Handler) http.Handler {
	return RequireRole("viewer", "operator", "admin")(next)
}

// RequireRole checks that the session role is one of the explicitly allowed
// roles. Unknown or missing roles fail closed.
func RequireRole(allowed ...string) func(http.Handler) http.Handler {
	allowedRoles := make(map[string]struct{}, len(allowed))
	for _, role := range allowed {
		allowedRoles[role] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, _ := r.Context().Value(middleware.ContextKeyUserRole).(string)
			if _, ok := allowedRoles[role]; !ok {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the direct client IP. Forwarding headers are considered
// only for explicitly trusted proxy peers.
func clientIP(r *http.Request, runtime *runtimeconfig.Runtime) string {
	if runtime != nil && runtime.RemoteIsTrustedProxy(r.RemoteAddr) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			hops := strings.Split(xff, ",")
			for i := len(hops) - 1; i >= 0; i-- {
				hop := strings.TrimSpace(hops[i])
				if net.ParseIP(hop) == nil {
					return directClientIP(r.RemoteAddr)
				}
				if !runtime.RemoteIsTrustedProxy(hop) {
					return hop
				}
			}
			return directClientIP(r.RemoteAddr)
		}
		if xri := r.Header.Get("X-Real-Ip"); xri != "" {
			xri = strings.TrimSpace(xri)
			if net.ParseIP(xri) != nil {
				return xri
			}
		}
	}
	return directClientIP(r.RemoteAddr)
}

func directClientIP(remoteAddr string) string {
	ip, _, _ := net.SplitHostPort(remoteAddr)
	if ip == "" {
		return remoteAddr
	}
	return ip
}
