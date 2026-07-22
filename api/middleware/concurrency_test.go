package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimitConcurrentRejectsAndReleasesSlowConnections(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	limiter := LimitConcurrent(1)
	handler := limiter(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))

	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		req := httptest.NewRequest(http.MethodGet, "/sse", nil).WithContext(ctx)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first connection did not enter handler")
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/sse", nil))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("saturated status = %d, want %d", second.Code, http.StatusServiceUnavailable)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("saturated response did not include Retry-After")
	}

	crud := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	crudResponse := httptest.NewRecorder()
	crud.ServeHTTP(crudResponse, httptest.NewRequest(http.MethodGet, "/crud", nil))
	if crudResponse.Code != http.StatusNoContent {
		t.Fatalf("independent CRUD status = %d, want %d", crudResponse.Code, http.StatusNoContent)
	}

	cancel()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("canceled connection did not release its slot")
	}

	quick := limiter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	third := httptest.NewRecorder()
	quick.ServeHTTP(third, httptest.NewRequest(http.MethodGet, "/sse", nil))
	if third.Code != http.StatusNoContent {
		t.Fatalf("released status = %d, want %d", third.Code, http.StatusNoContent)
	}
}

func TestLimitConcurrentForExpiresAndReleasesConnection(t *testing.T) {
	t.Parallel()

	handler := LimitConcurrentFor(1, 20*time.Millisecond)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/sse", nil))
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maximum connection lifetime did not cancel the handler")
	}
}

func TestLimitConcurrentRejectsInvalidLimit(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("LimitConcurrent(0) did not panic")
		}
	}()
	LimitConcurrent(0)
}

func TestSSELimitIsolatesRolesAndUsers(t *testing.T) {
	entered := make(chan struct{})
	limiter := LimitSSEConcurrentFor(2, 1, 1, 1, 0)
	handler := limiter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hold" {
			close(entered)
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := func(ctx context.Context, path, user, role string) *http.Request {
		ctx = context.WithValue(ctx, ContextKeyUserID, user)
		ctx = context.WithValue(ctx, ContextKeyUserRole, role)
		return httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), request(ctx, "/hold", "viewer-a", "viewer"))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("viewer did not occupy SSE slot")
	}

	sameUser := httptest.NewRecorder()
	handler.ServeHTTP(sameUser, request(context.Background(), "/quick", "viewer-a", "viewer"))
	if sameUser.Code != http.StatusServiceUnavailable {
		t.Fatalf("same-user status = %d, want %d", sameUser.Code, http.StatusServiceUnavailable)
	}
	otherViewer := httptest.NewRecorder()
	handler.ServeHTTP(otherViewer, request(context.Background(), "/quick", "viewer-b", "viewer"))
	if otherViewer.Code != http.StatusNoContent {
		t.Fatalf("other viewer status = %d, want %d", otherViewer.Code, http.StatusNoContent)
	}
	admin := httptest.NewRecorder()
	handler.ServeHTTP(admin, request(context.Background(), "/quick", "admin-a", "admin"))
	if admin.Code != http.StatusNoContent {
		t.Fatalf("admin status = %d, want %d", admin.Code, http.StatusNoContent)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("viewer slot was not released")
	}
}
