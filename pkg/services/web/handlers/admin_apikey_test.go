package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	apiserver "github.com/Constellation-Overwatch/constellation-overwatch/api"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/db"
	adminpages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/admin/pages"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

func newAPIKeyConformanceDB(t *testing.T) *db.Service {
	t.Helper()

	dbSvc, err := db.New(&db.Config{
		DBPath:         filepath.Join(t.TempDir(), "api-key-conformance.db"),
		MaxOpenConns:   1,
		MaxIdleConns:   1,
		AutoInitialize: true,
	})
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		if err := dbSvc.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})

	if _, err := dbSvc.DB.Exec(
		`INSERT INTO organizations (org_id, name, org_type)
		 VALUES
		 ('org-a', 'Organization A', 'commercial'),
		 ('org-b', 'Organization B', 'commercial')`,
	); err != nil {
		t.Fatalf("insert organizations: %v", err)
	}
	if _, err := dbSvc.DB.Exec(
		`INSERT INTO users (user_id, org_id, username, email, role)
		 VALUES ('admin-a', 'org-a', 'admin-a', 'admin-a@example.test', 'admin')`,
	); err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	return dbSvc
}

func adminAPIKeyRequest(t *testing.T, body string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/admin/apikeys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserRole, shared.RoleAdmin)
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "admin-a")
	ctx = context.WithValue(ctx, middleware.ContextKeyOrgID, "org-a")
	return req.WithContext(ctx)
}

func TestUIIssuedLeastPrivilegeOrganizationKeyPassesOrganizationAPI(t *testing.T) {
	const keyHashSecret = "test-only-api-key-secret-at-least-32-bytes"

	dbSvc := newAPIKeyConformanceDB(t)
	handler := NewAdminHandler(
		nil,
		services.NewAPIKeyServiceWithSecret(dbSvc.DB, keyHashSecret),
		nil,
		nil,
	)

	rec := httptest.NewRecorder()
	handler.HandleCreateAPIKey(rec, adminAPIKeyRequest(
		t,
		`{"name":"organization-reader","scopes":["organizations:read"]}`,
	))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create API key status=%d body=%s", rec.Code, rec.Body.String())
	}

	var createResponse struct {
		Success bool                `json:"success"`
		Data    services.CreatedKey `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &createResponse); err != nil {
		t.Fatalf("decode API key response: %v", err)
	}
	if !createResponse.Success || createResponse.Data.APIKey == "" {
		t.Fatalf("create API key response = %+v", createResponse)
	}
	if len(createResponse.Data.Scopes) != 1 ||
		createResponse.Data.Scopes[0] != shared.ScopeOrganizationsRead {
		t.Fatalf("created scopes = %v", createResponse.Data.Scopes)
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/v1/organizations", nil)
	apiReq.Header.Set("X-API-Key", createResponse.Data.APIKey)
	apiRec := httptest.NewRecorder()
	apiserver.NewRouterWithRuntimeSecurity(dbSvc.DB, nil, nil, keyHashSecret).ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("organization API status=%d body=%s", apiRec.Code, apiRec.Body.String())
	}
	if bytes.Contains(apiRec.Body.Bytes(), []byte(`"Organization B"`)) {
		t.Fatalf("organization API response crossed the key's organization: %s", apiRec.Body.String())
	}

	var storedScopes string
	if err := dbSvc.DB.QueryRow(
		`SELECT scopes FROM api_keys WHERE key_id = ?`,
		createResponse.Data.KeyID,
	).Scan(&storedScopes); err != nil {
		t.Fatalf("read stored scopes: %v", err)
	}
	if storedScopes != shared.ScopeOrganizationsRead {
		t.Fatalf("stored scopes = %q, want %q", storedScopes, shared.ScopeOrganizationsRead)
	}

	schemaReq := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	schemaRec := httptest.NewRecorder()
	apiserver.NewRouter(dbSvc.DB, nil).ServeHTTP(schemaRec, schemaReq)
	if schemaRec.Code != http.StatusOK {
		t.Fatalf("OpenAPI status=%d body=%s", schemaRec.Code, schemaRec.Body.String())
	}
	if !strings.Contains(schemaRec.Body.String(), shared.ScopeOrganizationsRead) ||
		strings.Contains(schemaRec.Body.String(), `"orgs:read"`) {
		t.Fatal("OpenAPI does not use only the canonical organization scope")
	}
}

func TestAdminAPIKeyCreationRejectsDeprecatedAndUnknownScopes(t *testing.T) {
	const keyHashSecret = "test-only-api-key-secret-at-least-32-bytes"
	dbSvc := newAPIKeyConformanceDB(t)
	handler := NewAdminHandler(
		nil,
		services.NewAPIKeyServiceWithSecret(dbSvc.DB, keyHashSecret),
		nil,
		nil,
	)

	for _, scope := range []string{"orgs:read", "organizations:reed", "nats:all"} {
		t.Run(scope, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.HandleCreateAPIKey(rec, adminAPIKeyRequest(
				t,
				`{"name":"invalid","scopes":["`+scope+`"]}`,
			))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"code":"INVALID_SCOPE"`) {
				t.Fatalf("response does not identify invalid scope: %s", rec.Body.String())
			}
		})
	}

	var count int
	if err := dbSvc.DB.QueryRow(`SELECT COUNT(*) FROM api_keys`).Scan(&count); err != nil {
		t.Fatalf("count API keys: %v", err)
	}
	if count != 0 {
		t.Fatalf("invalid scope requests created %d API keys", count)
	}
}

func TestAdminPageEmitsCanonicalOrganizationScopes(t *testing.T) {
	var rendered bytes.Buffer
	if err := adminpages.AdminPage().Render(context.Background(), &rendered); err != nil {
		t.Fatalf("render admin page: %v", err)
	}

	html := rendered.String()
	for _, scope := range []string{
		shared.ScopeOrganizationsRead,
		shared.ScopeOrganizationsWrite,
		shared.ScopeNATSTelemetryWrite,
		shared.ScopeNATSCommandsRead,
		shared.ScopeNATSCommandsWrite,
		shared.ScopeNATSEventsWrite,
	} {
		if !strings.Contains(html, `value="`+scope+`"`) {
			t.Fatalf("admin page does not emit canonical scope %q", scope)
		}
	}
	for _, deprecated := range []string{`value="orgs:`, `value="nats:all"`, `value="nats:telemetry"`} {
		if strings.Contains(html, deprecated) {
			t.Fatalf("admin page emits deprecated scope pattern %q", deprecated)
		}
	}
}
