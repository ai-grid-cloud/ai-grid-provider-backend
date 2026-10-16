package restgateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/osac-project/ai-grid-provider-backend/internal/keycloak"
	"github.com/osac-project/ai-grid-provider-backend/internal/restgateway"
	"github.com/osac-project/ai-grid-provider-backend/internal/servers"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

var managedClustersResource = schema.GroupVersionResource{
	Group: "cluster.open-cluster-management.io", Version: "v1", Resource: "managedclusters",
}

func TestRESTGatewayRoutes(t *testing.T) {
	t.Parallel()

	baseURL := startGateway(t,
		[]runtime.Object{
			managedCluster("seed-cluster", map[string]string{"environment": "dev"}, true),
		},
		fakeOrganizationLister{
			orgs: []keycloak.Organization{
				{ID: "org-1", Name: "tenant-a", Domains: []keycloak.OrganizationDomain{{Name: "a.example.com"}}},
			},
		},
	)

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	t.Run("ping", func(t *testing.T) {
		body := mustGET(t, client, baseURL+"/api/ai-grid/v1/ping?name=Ada")
		if !strings.Contains(body, "Welcome, Ada!") {
			t.Fatalf("ping body = %q", body)
		}
	})

	t.Run("list managed clusters", func(t *testing.T) {
		body := mustGET(t, client, baseURL+"/api/ai-grid/v1/managed_clusters")
		var payload map[string]any
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("unmarshal list: %v (%s)", err, body)
		}
		if _, ok := payload["items"]; !ok {
			t.Fatalf("missing items: %s", body)
		}
		if payload["size"] == nil || payload["total"] == nil {
			t.Fatalf("missing size/total: %s", body)
		}
		if !strings.Contains(body, `"seed-cluster"`) {
			t.Fatalf("list missing seed-cluster: %s", body)
		}
	})

	t.Run("list clusters presentation", func(t *testing.T) {
		body := mustGET(t, client, baseURL+"/api/ai-grid/v1/clusters")
		var items []map[string]any
		if err := json.Unmarshal([]byte(body), &items); err != nil {
			t.Fatalf("unmarshal clusters list (want bare array): %v (%s)", err, body)
		}
		if len(items) != 1 {
			t.Fatalf("clusters len = %d (%s)", len(items), body)
		}
		item := items[0]
		if item["name"] != "seed-cluster" {
			t.Fatalf("name = %v (%s)", item["name"], body)
		}
		if _, ok := item["ai_grid_enabled"]; !ok {
			t.Fatalf("missing ai_grid_enabled: %s", body)
		}
		if _, ok := item["provisioning_status"]; !ok {
			t.Fatalf("missing provisioning_status: %s", body)
		}
		if _, ok := item["status"]; !ok {
			t.Fatalf("missing status: %s", body)
		}
	})

	t.Run("get managed cluster", func(t *testing.T) {
		body := mustGET(t, client, baseURL+"/api/ai-grid/v1/managed_clusters/seed-cluster")
		var payload map[string]any
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("unmarshal get: %v (%s)", err, body)
		}
		if payload["id"] != "seed-cluster" {
			t.Fatalf("id = %v, want seed-cluster (%s)", payload["id"], body)
		}
		metadata, _ := payload["metadata"].(map[string]any)
		if metadata["name"] != "seed-cluster" {
			t.Fatalf("metadata.name = %v (%s)", metadata["name"], body)
		}
		spec, _ := payload["spec"].(map[string]any)
		if spec["hub_accepts_client"] != true {
			t.Fatalf("spec.hub_accepts_client = %v (%s)", spec["hub_accepts_client"], body)
		}
	})

	t.Run("list tenants", func(t *testing.T) {
		body := mustGET(t, client, baseURL+"/api/ai-grid/v1/tenants")
		if !strings.Contains(body, `"tenant-a"`) || !strings.Contains(body, `"org-1"`) {
			t.Fatalf("tenants body = %s", body)
		}
		if !strings.Contains(body, `"items"`) {
			t.Fatalf("missing items envelope: %s", body)
		}
	})

	t.Run("openapi", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/openapi.json")
		if err != nil {
			t.Fatalf("GET openapi.json: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("Content-Type = %q", ct)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !bytes.Contains(body, []byte(`"swagger"`)) {
			t.Fatalf("openapi missing swagger: %s", body)
		}
	})

	t.Run("docs", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/docs/")
		if err != nil {
			t.Fatalf("GET /docs/: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !bytes.Contains(bytes.ToLower(body), []byte("html")) {
			t.Fatalf("docs body not html: %s", body)
		}

		redirect, err := client.Get(baseURL + "/docs")
		if err != nil {
			t.Fatalf("GET /docs: %v", err)
		}
		defer redirect.Body.Close()
		if redirect.StatusCode != http.StatusFound && redirect.StatusCode != http.StatusMovedPermanently {
			t.Fatalf("/docs status = %d, want redirect", redirect.StatusCode)
		}
		if loc := redirect.Header.Get("Location"); !strings.HasSuffix(loc, "/docs/") {
			t.Fatalf("Location = %q, want .../docs/", loc)
		}
	})
}

func TestRESTClustersEnroll(t *testing.T) {
	t.Parallel()

	baseURL := startGateway(t,
		[]runtime.Object{
			managedCluster("cluster-b", map[string]string{"location": "site2", "team": "platform"}, true),
		},
		fakeOrganizationLister{},
	)
	client := &http.Client{Timeout: 10 * time.Second}

	// Proto JSON accepts camelCase on unmarshal; gateway marshals snake_case.
	req, err := http.NewRequest(http.MethodPatch, baseURL+"/api/ai-grid/v1/clusters/cluster-b", strings.NewReader(`{"aiGridEnabled":true}`))
	if err != nil {
		t.Fatalf("new enroll request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PATCH enroll: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read enroll body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enroll status = %d body = %s", resp.StatusCode, body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal enroll: %v (%s)", err, body)
	}
	if got["name"] != "cluster-b" || got["ai_grid_enabled"] != true || got["provisioning_status"] != "Provisioning" {
		t.Fatalf("enroll body = %s", body)
	}
	if got["location"] != "site2" {
		t.Fatalf("location not preserved: %s", body)
	}

	listBody := mustGET(t, client, baseURL+"/api/ai-grid/v1/clusters")
	if !strings.Contains(listBody, `"ai_grid_enabled":true`) {
		t.Fatalf("list after enroll = %s", listBody)
	}
}

func TestRESTManagedClusterCreateDelete(t *testing.T) {
	t.Parallel()

	baseURL := startGateway(t, nil, fakeOrganizationLister{})
	client := &http.Client{Timeout: 10 * time.Second}
	const id = "rest-crud-demo"

	createBody := `{
		"id":"` + id + `",
		"metadata":{"name":"` + id + `","labels":{"environment":"smoke"}},
		"spec":{"hub_accepts_client":true}
	}`
	resp, err := client.Post(baseURL+"/api/ai-grid/v1/managed_clusters", "application/json", strings.NewReader(createBody))
	if err != nil {
		t.Fatalf("POST create: %v", err)
	}
	defer resp.Body.Close()
	created, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read create body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d body = %s", resp.StatusCode, created)
	}
	if !strings.Contains(string(created), `"`+id+`"`) {
		t.Fatalf("create body missing id: %s", created)
	}

	got := mustGET(t, client, baseURL+"/api/ai-grid/v1/managed_clusters/"+id)
	if !strings.Contains(got, `"environment"`) || !strings.Contains(got, `"smoke"`) {
		t.Fatalf("get after create = %s", got)
	}

	delReq, err := http.NewRequest(http.MethodDelete, baseURL+"/api/ai-grid/v1/managed_clusters/"+id, nil)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	delResp, err := client.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(delResp.Body)
		t.Fatalf("delete status = %d body = %s", delResp.StatusCode, body)
	}

	missing, err := client.Get(baseURL + "/api/ai-grid/v1/managed_clusters/" + id)
	if err != nil {
		t.Fatalf("GET after delete: %v", err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(missing.Body)
		t.Fatalf("get after delete status = %d body = %s", missing.StatusCode, body)
	}
}

func startGateway(t *testing.T, objects []runtime.Object, orgs fakeOrganizationLister) string {
	t.Helper()

	if objects == nil {
		objects = []runtime.Object{}
	}
	dynamicClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			managedClustersResource: "ManagedClusterList",
		},
		objects...,
	)

	aiGridServer, err := servers.NewAiGridServer().Build()
	if err != nil {
		t.Fatalf("build AiGrid server: %v", err)
	}
	managedClustersServer, err := servers.NewManagedClustersServer().
		SetDynamicClient(dynamicClient).
		Build()
	if err != nil {
		t.Fatalf("build ManagedClusters server: %v", err)
	}
	clustersServer, err := servers.NewClustersServer().
		SetManagedClusters(managedClustersServer).
		Build()
	if err != nil {
		t.Fatalf("build Clusters server: %v", err)
	}
	tenantsServer, err := servers.NewTenantsServer().
		SetOrganizationLister(orgs).
		Build()
	if err != nil {
		t.Fatalf("build Tenants server: %v", err)
	}

	ctx := context.Background()
	gateway, err := restgateway.New(ctx, "127.0.0.1:0", restgateway.Servers{
		AiGrid:          aiGridServer,
		ManagedClusters: managedClustersServer,
		Clusters:        clustersServer,
		Tenants:         tenantsServer,
	})
	if err != nil {
		t.Fatalf("restgateway.New: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- gateway.Serve()
	}()

	baseURL := "http://" + gateway.Addr()
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := http.Get(baseURL + "/openapi.json")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway did not become ready: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = gateway.Shutdown(shutdownCtx)
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
		}
	})
	return baseURL
}

func mustGET(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d body = %s", url, resp.StatusCode, body)
	}
	return string(body)
}

type fakeOrganizationLister struct {
	orgs []keycloak.Organization
	err  error
}

func (f fakeOrganizationLister) ListOrganizations(context.Context) ([]keycloak.Organization, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.orgs, nil
}

func managedCluster(name string, labels map[string]string, accepts bool) *unstructured.Unstructured {
	jsonLabels := make(map[string]interface{}, len(labels))
	for key, value := range labels {
		jsonLabels[key] = value
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cluster.open-cluster-management.io/v1",
		"kind":       "ManagedCluster",
		"metadata": map[string]interface{}{
			"name":   name,
			"labels": jsonLabels,
		},
		"spec": map[string]interface{}{
			"hubAcceptsClient": accepts,
		},
	}}
}
