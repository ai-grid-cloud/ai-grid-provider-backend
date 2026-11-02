package keycloak

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateIdentityProvider(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/token"):
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/identity-provider/instances"):
			gotMethod, gotPath = r.Method, r.URL.Path
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := testClient(server)
	err := client.CreateIdentityProvider(context.Background(), IdentityProvider{
		Alias: "acme", DisplayName: "Acme", ProviderID: "oidc", Enabled: true,
		Config: map[string]string{"clientId": "c", "clientSecret": "s"},
	})
	if err != nil {
		t.Fatalf("CreateIdentityProvider: %v", err)
	}
	if gotMethod != http.MethodPost || !strings.HasSuffix(gotPath, "/identity-provider/instances") {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if gotBody["alias"] != "acme" || gotBody["providerId"] != "oidc" {
		t.Fatalf("body = %#v", gotBody)
	}
}

func TestDeleteIdentityProvider(t *testing.T) {
	t.Parallel()
	t.Run("ok", func(t *testing.T) {
		t.Parallel()
		var gotMethod, gotPath string
		server := httptest.NewServer(tokenThen(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(server.Close)
		if err := testClient(server).DeleteIdentityProvider(context.Background(), "acme"); err != nil {
			t.Fatalf("DeleteIdentityProvider: %v", err)
		}
		if gotMethod != http.MethodDelete || !strings.HasSuffix(gotPath, "/identity-provider/instances/acme") {
			t.Fatalf("request = %s %s", gotMethod, gotPath)
		}
	})
	t.Run("404 is success", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(tokenThen(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "missing", http.StatusNotFound)
		}))
		t.Cleanup(server.Close)
		if err := testClient(server).DeleteIdentityProvider(context.Background(), "acme"); err != nil {
			t.Fatalf("DeleteIdentityProvider 404: %v", err)
		}
	})
}

func TestLinkOrganizationIdentityProvider(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath, gotBody string
	server := httptest.NewServer(tokenThen(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	if err := testClient(server).LinkOrganizationIdentityProvider(context.Background(), "org-1", "acme"); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if gotMethod != http.MethodPost || !strings.HasSuffix(gotPath, "/organizations/org-1/identity-providers") {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if gotBody != `"acme"` {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestConfigureOrganizationIdentityProvider(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	var gotBody OrganizationIdentityProviderLink
	server := httptest.NewServer(tokenThen(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	if err := testClient(server).ConfigureOrganizationIdentityProvider(context.Background(), "org-1", "acme"); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if gotMethod != http.MethodPut || !strings.HasSuffix(gotPath, "/organizations/org-1/identity-providers/acme") {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if !gotBody.AutoMembership || gotBody.MembershipType != "MANAGED" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestUpsertIdentityProviderMapper(t *testing.T) {
	t.Parallel()
	t.Run("create when missing", func(t *testing.T) {
		t.Parallel()
		var createBody IdentityProviderMapper
		var created bool
		server := httptest.NewServer(tokenThen(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/mappers"):
				_ = json.NewEncoder(w).Encode([]IdentityProviderMapper{})
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/mappers"):
				created = true
				_ = json.NewDecoder(r.Body).Decode(&createBody)
				w.WriteHeader(http.StatusCreated)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)
		if err := testClient(server).UpsertIdentityProviderMapper(context.Background(), "acme", "uuid-1"); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if !created || createBody.Name != "AI Grid org id" || createBody.Config["attribute"] != "maas_org_id" || createBody.Config["attribute.value"] != "uuid-1" {
			t.Fatalf("created=%v body=%+v", created, createBody)
		}
	})
	t.Run("update when present", func(t *testing.T) {
		t.Parallel()
		var updatePath string
		var updateBody IdentityProviderMapper
		server := httptest.NewServer(tokenThen(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/mappers"):
				_ = json.NewEncoder(w).Encode([]IdentityProviderMapper{{
					ID: "mapper-1", Name: "AI Grid org id", IdentityProviderAlias: "acme",
				}})
			case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/mappers/mapper-1"):
				updatePath = r.URL.Path
				_ = json.NewDecoder(r.Body).Decode(&updateBody)
				w.WriteHeader(http.StatusNoContent)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)
		if err := testClient(server).UpsertIdentityProviderMapper(context.Background(), "acme", "uuid-2"); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if !strings.HasSuffix(updatePath, "/mappers/mapper-1") || updateBody.Config["attribute.value"] != "uuid-2" {
			t.Fatalf("path=%s body=%+v", updatePath, updateBody)
		}
	})
}

func testClient(server *httptest.Server) *Client {
	return New(Config{
		BaseURL: server.URL, AdminRealm: "master", Realm: "ai-grid",
		Username: "admin", Password: "secret", HTTPClient: server.Client(),
	})
}

func tokenThen(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/token") {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
			return
		}
		next(w, r)
	}
}
