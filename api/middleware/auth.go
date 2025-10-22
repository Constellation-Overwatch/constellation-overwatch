package middleware

import (
	"constellation-api/pkg/shared"
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// BearerAuth middleware for API authentication
func BearerAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get the bearer token from environment or use default
		validToken := os.Getenv("API_BEARER_TOKEN")
		if validToken == "" {
			validToken = "constellation-dev-token" // Default for development
		}

		// Extract token from Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			sendUnauthorized(w, "Missing authorization header")
			return
		}

		// Check if it's a bearer token
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			sendUnauthorized(w, "Invalid authorization format")
			return
		}

		token := parts[1]
		if token != validToken {
			sendUnauthorized(w, "Invalid token")
			return
		}

		// Token is valid, proceed with the request
		next(w, r)
	}
}

// OptionalAuth middleware - allows both authenticated and unauthenticated requests
func OptionalAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			// If auth header is present, validate it
			BearerAuth(next)(w, r)
		} else {
			// No auth header, proceed anyway
			next(w, r)
		}
	}
}

func sendUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	
	response := shared.Response{
		Success: false,
		Error: &shared.Error{
			Code:    "UNAUTHORIZED",
			Message: message,
		},
	}
	
	json.NewEncoder(w).Encode(response)
}

// CORS middleware for handling cross-origin requests
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		// Handle preflight requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RequestLogger middleware for logging requests
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simple request logging
		// In production, you'd want more sophisticated logging
		next.ServeHTTP(w, r)
	})
}