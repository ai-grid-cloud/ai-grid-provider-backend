package hub

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// The hub serves ConfigMaps from its directory and reads the file on every
// GET, so a file added or edited while it runs applies at once.
func TestConfigMapsReadOnEveryGet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fx := testFixtures()
	fx.Access.ServiceAccount.Token = testToken
	h, err := New(fx, WithLogger(slog.New(slog.DiscardHandler)), WithConfigMapDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)
	client, err := dynamic.NewForConfig(&rest.Config{Host: srv.URL, BearerToken: testToken})
	if err != nil {
		t.Fatal(err)
	}
	configMaps := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).Namespace("ai-grid")
	ctx := context.Background()

	if _, err := configMaps.Get(ctx, "options", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Get(no file) error = %v, want NotFound", err)
	}
	write := func(value string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, "ai-grid"), 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: options, namespace: ai-grid}\ndata: {key: " + value + "}\n"
		if err := os.WriteFile(filepath.Join(dir, "ai-grid", "options.yaml"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// An escaped "/" in the path can't leave the directory.
	if err := os.WriteFile(filepath.Join(dir, "secret.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/v1/namespaces/ai-grid/configmaps/..%2Fsecret",
		"/api/v1/namespaces/..%2F..%2Fx/configmaps/secret",
	} {
		resp, err := srv.Client().Do(func() *http.Request {
			req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
			req.Header.Set("Authorization", "Bearer "+testToken)
			return req
		}())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
	for _, value := range []string{"one", "two"} {
		write(value)
		got, err := configMaps.Get(ctx, "options", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if data, _ := got.Object["data"].(map[string]any); data["key"] != value {
			t.Fatalf("data = %v, want key %s", got.Object["data"], value)
		}
	}
}
