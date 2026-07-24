package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/db"
	"github.com/go-chi/chi/v5"
)

type authorizationTestServices struct {
	orgs     *services.OrganizationService
	entities *services.EntityService
	db       *db.Service
}

func newAuthorizationTestServices(t *testing.T) authorizationTestServices {
	t.Helper()

	dbSvc, err := db.New(&db.Config{
		DBPath:         filepath.Join(t.TempDir(), "authorization.db"),
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

	for _, org := range []struct {
		id   string
		name string
	}{
		{id: "org-a", name: "Organization A"},
		{id: "org-b", name: "Organization B"},
	} {
		if _, err := dbSvc.DB.Exec(
			`INSERT INTO organizations (org_id, name, org_type) VALUES (?, ?, 'commercial')`,
			org.id,
			org.name,
		); err != nil {
			t.Fatalf("insert %s: %v", org.id, err)
		}
	}

	for _, entity := range []struct {
		id    string
		orgID string
		name  string
	}{
		{id: "entity-a", orgID: "org-a", name: "Entity A"},
		{id: "entity-b", orgID: "org-b", name: "Entity B"},
	} {
		if _, err := dbSvc.DB.Exec(
			`INSERT INTO entities (entity_id, org_id, name, entity_type)
			 VALUES (?, ?, ?, 'sensor_platform')`,
			entity.id,
			entity.orgID,
			entity.name,
		); err != nil {
			t.Fatalf("insert %s: %v", entity.id, err)
		}
	}

	return authorizationTestServices{
		orgs:     services.NewOrganizationService(dbSvc.DB, nil),
		entities: services.NewEntityService(dbSvc.DB, nil),
		db:       dbSvc,
	}
}

func requestWithSession(
	t *testing.T,
	method string,
	target string,
	body *strings.Reader,
	role string,
	orgID string,
	urlParams map[string]string,
) *http.Request {
	t.Helper()

	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, body)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyOrgID, orgID)
	if len(urlParams) > 0 {
		routeCtx := chi.NewRouteContext()
		for key, value := range urlParams {
			routeCtx.URLParams.Add(key, value)
		}
		ctx = context.WithValue(ctx, chi.RouteCtxKey, routeCtx)
	}
	return req.WithContext(ctx)
}

func TestSessionReadsAreScopedToSessionOrganization(t *testing.T) {
	svcs := newAuthorizationTestServices(t)
	handler := NewDatastarHandler(svcs.orgs, svcs.entities, nil)

	req := requestWithSession(t, http.MethodGet, "/api/organizations/", nil, "viewer", "org-a", nil)
	rec := httptest.NewRecorder()
	handler.HandleListOrganizations(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list organizations status=%d body=%s", rec.Code, rec.Body.String())
	}
	var orgResponse struct {
		Organizations []struct {
			OrgID string `json:"org_id"`
		} `json:"organizations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &orgResponse); err != nil {
		t.Fatalf("decode organizations: %v", err)
	}
	if len(orgResponse.Organizations) != 1 || orgResponse.Organizations[0].OrgID != "org-a" {
		t.Fatalf("viewer organizations=%+v, want only org-a", orgResponse.Organizations)
	}

	req = requestWithSession(t, http.MethodGet, "/api/entities/", nil, "viewer", "org-a", nil)
	rec = httptest.NewRecorder()
	handler.HandleListEntities(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list entities status=%d body=%s", rec.Code, rec.Body.String())
	}
	var entityResponse struct {
		Entities []struct {
			EntityID string `json:"entity_id"`
			OrgID    string `json:"org_id"`
		} `json:"entities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &entityResponse); err != nil {
		t.Fatalf("decode entities: %v", err)
	}
	if len(entityResponse.Entities) != 1 ||
		entityResponse.Entities[0].EntityID != "entity-a" ||
		entityResponse.Entities[0].OrgID != "org-a" {
		t.Fatalf("viewer entities=%+v, want only entity-a", entityResponse.Entities)
	}
}

func TestDirectBrowserMutationsEnforceRoleAndOrganization(t *testing.T) {
	svcs := newAuthorizationTestServices(t)
	handler := NewDatastarHandler(svcs.orgs, svcs.entities, nil)

	req := requestWithSession(
		t,
		http.MethodPost,
		"/api/organizations/",
		strings.NewReader(url.Values{"name": {"Blocked Organization"}}.Encode()),
		"operator",
		"org-a",
		nil,
	)
	rec := httptest.NewRecorder()
	handler.HandleCreateOrganization(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("operator organization mutation status=%d, want 403", rec.Code)
	}

	form := url.Values{"name": {"Blocked"}, "entity_type": {"sensor_platform"}}
	req = requestWithSession(
		t,
		http.MethodPost,
		"/api/entities/?org_id=org-a",
		strings.NewReader(form.Encode()),
		"viewer",
		"org-a",
		nil,
	)
	rec = httptest.NewRecorder()
	handler.HandleCreateEntity(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer mutation status=%d, want 403", rec.Code)
	}

	req = requestWithSession(
		t,
		http.MethodPut,
		"/api/entities/entity-b?org_id=org-b",
		strings.NewReader(url.Values{"name": {"Compromised"}}.Encode()),
		"operator",
		"org-a",
		map[string]string{"entity_id": "entity-b"},
	)
	rec = httptest.NewRecorder()
	handler.HandleUpdateEntity(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org mutation status=%d body=%s, want 404", rec.Code, rec.Body.String())
	}

	var name string
	if err := svcs.db.DB.QueryRow(
		`SELECT name FROM entities WHERE entity_id = 'entity-b'`,
	).Scan(&name); err != nil {
		t.Fatalf("read protected entity: %v", err)
	}
	if name != "Entity B" {
		t.Fatalf("cross-org entity changed to %q", name)
	}

	req = requestWithSession(
		t,
		http.MethodPut,
		"/api/entities/entity-a?org_id=org-a",
		strings.NewReader(url.Values{"name": {"Updated by Operator"}}.Encode()),
		"operator",
		"org-a",
		map[string]string{"entity_id": "entity-a"},
	)
	rec = httptest.NewRecorder()
	handler.HandleUpdateEntity(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("own-org operator mutation status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := svcs.db.DB.QueryRow(
		`SELECT name FROM entities WHERE entity_id = 'entity-a'`,
	).Scan(&name); err != nil {
		t.Fatalf("read updated entity: %v", err)
	}
	if name != "Updated by Operator" {
		t.Fatalf("own-org entity name=%q", name)
	}
}

func TestAdminCanReadAcrossOrganizations(t *testing.T) {
	svcs := newAuthorizationTestServices(t)
	handler := NewDatastarHandler(svcs.orgs, svcs.entities, nil)

	req := requestWithSession(t, http.MethodGet, "/api/entities/", nil, "admin", "org-a", nil)
	rec := httptest.NewRecorder()
	handler.HandleListEntities(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list status=%d body=%s", rec.Code, rec.Body.String())
	}

	var response struct {
		Entities []json.RawMessage `json:"entities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode admin entities: %v", err)
	}
	if len(response.Entities) != 2 {
		t.Fatalf("admin entity count=%d, want 2", len(response.Entities))
	}
}
