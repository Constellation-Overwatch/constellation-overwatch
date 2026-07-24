package web

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	runtimeconfig "github.com/Constellation-Overwatch/constellation-overwatch/pkg/config"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/a-h/templ"
)

// SecurityHeaders adds security response headers to all responses.
func SecurityHeaders(next http.Handler) http.Handler {
	return SecurityHeadersForConfig(runtimeconfig.Development())(next)
}

// SecurityHeadersForConfig returns headers bound to the validated runtime
// profile. HSTS is emitted only for the HTTPS production profile.
func SecurityHeadersForConfig(cfg runtimeconfig.Runtime) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nonce, err := newCSPNonce()
			if err != nil {
				logger.Errorw("Failed to generate CSP nonce", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
			w.Header().Set("X-XSS-Protection", "0") // Disabled per modern best practice
			csp := strings.Replace(
				cfg.ContentSecurityPolicy,
				"script-src 'self'",
				fmt.Sprintf("script-src 'self' 'nonce-%s'", nonce),
				1,
			)
			w.Header().Set("Content-Security-Policy", csp)
			if cfg.HSTS {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r.WithContext(templ.WithNonce(r.Context(), nonce)))
		})
	}
}

func newCSPNonce() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
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
	return RateLimitByIPFromTrustedProxies(requestsPerMinute, nil)
}

// RateLimitByIPFromTrustedProxies honors forwarding headers only when the
// direct peer belongs to an explicitly configured proxy prefix.
func RateLimitByIPFromTrustedProxies(requestsPerMinute int, trustedProxies []netip.Prefix) func(http.Handler) http.Handler {
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
			ip := clientIPFromTrustedProxies(r, trustedProxies)
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
	return RequireRole(shared.RoleAdmin)(next)
}

// RequireRole permits only authenticated sessions whose role is explicitly
// listed. It is intentionally deny-by-default for missing or unknown roles.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := middleware.UserRoleFromContext(r.Context())
			if _, ok := allowed[role]; !ok {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the client IP from the request, checking proxy headers.
func clientIP(r *http.Request) string {
	return clientIPFromTrustedProxies(r, nil)
}

func clientIPFromTrustedProxies(r *http.Request, trustedProxies []netip.Prefix) string {
	remoteIP := remoteAddressIP(r.RemoteAddr)
	trusted := false
	if parsed, err := netip.ParseAddr(remoteIP); err == nil {
		for _, prefix := range trustedProxies {
			if prefix.Contains(parsed) {
				trusted = true
				break
			}
		}
	}
	if !trusted {
		return remoteIP
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		hops := strings.Split(xff, ",")
		for i := len(hops) - 1; i >= 0; i-- {
			candidate := strings.TrimSpace(hops[i])
			if !validIPAddress(candidate) {
				return remoteIP
			}
			if !addressInPrefixes(candidate, trustedProxies) {
				return candidate
			}
		}
		return remoteIP
	}
	if candidate := strings.TrimSpace(r.Header.Get("X-Real-Ip")); validIPAddress(candidate) {
		return candidate
	}
	return remoteIP
}

func addressInPrefixes(address string, prefixes []netip.Prefix) bool {
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	for _, prefix := range prefixes {
		if prefix.Contains(parsed) {
			return true
		}
	}
	return false
}

func remoteAddressIP(remoteAddr string) string {
	ip, _, _ := net.SplitHostPort(remoteAddr)
	if ip != "" {
		return ip
	}
	return remoteAddr
}

func validIPAddress(value string) bool {
	_, err := netip.ParseAddr(value)
	return err == nil
}
