package hub

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var testApplicationsGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

// The shipped catalog Applications load, and the hub lists and gets them by
// namespace.
func TestShippedApplications(t *testing.T) {
	t.Parallel()
	fx := loadShipped(t, "default")
	fx.Access.ServiceAccount.Token = testToken
	th := startHub(t, fx)
	client, err := dynamic.NewForConfig(&rest.Config{Host: th.srv.URL, BearerToken: testToken})
	if err != nil {
		t.Fatalf("dynamic.NewForConfig: %v", err)
	}
	apps := client.Resource(testApplicationsGVR)
	list, err := apps.Namespace("openshift-gitops").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	names := make([]string, 0, len(list.Items))
	for _, app := range list.Items {
		names = append(names, app.GetName())
		if owners := app.GetOwnerReferences(); len(owners) != 1 || owners[0].Kind != "ApplicationSet" {
			t.Errorf("%s ownerReferences = %v, want its ApplicationSet", app.GetName(), owners)
		}
	}
	want := []string{
		"acm-models-catalog-ingress-site-ashburn-1",
		"acm-models-catalog-models-site-madrid-1",
		"acm-models-catalog-models-site-paris-1",
		"acm-models-catalog-subscriptions-site-ashburn-1",
	}
	if len(names) != len(want) {
		t.Fatalf("List() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("List() = %v, want %v", names, want)
		}
	}
	got, err := apps.Namespace("openshift-gitops").Get(context.Background(), want[2], metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	// granite-3b, and one model per deployment state the console words:
	// stopped, failed, an edit not applied yet, and running with no
	// subscription.
	if resources, _, _ := unstructured.NestedSlice(got.Object, "status", "resources"); len(resources) != 10 {
		t.Fatalf("Get() status.resources = %v, want a NetworkPolicy and an LLMInferenceService for each of 5 models", resources)
	}
	if empty, err := apps.Namespace("argocd").List(context.Background(), metav1.ListOptions{}); err != nil || len(empty.Items) != 0 {
		t.Fatalf("List(another namespace) = %v, %v; want empty", empty, err)
	}
	if _, err := apps.Namespace("openshift-gitops").Get(context.Background(), "nope", metav1.GetOptions{}); err == nil {
		t.Fatal("Get(an Application the fixtures don't have) succeeded")
	}
}

func TestApplicationFixturesRejectDuplicatesAndMissingNames(t *testing.T) {
	t.Parallel()
	app := func(namespace, name string) unstructured.Unstructured {
		a := unstructured.Unstructured{}
		a.SetNamespace(namespace)
		a.SetName(name)
		return a
	}
	for name, apps := range map[string][]unstructured.Unstructured{
		"duplicate":    {app("a", "x"), app("a", "x")},
		"no name":      {app("a", "")},
		"no namespace": {app("", "x")},
	} {
		if _, err := New(&Fixtures{Applications: apps}); err == nil {
			t.Errorf("%s: New() succeeded", name)
		}
	}
}

// With WithSubscriptionIDs, each Application also reports every catalog
// subscription named base-<suffix> (and its -policy) as it reports base (and
// base-policy); other ids, and other kinds, add nothing.
func TestApplicationsReportGeneratedSubscriptionNames(t *testing.T) {
	t.Parallel()
	fx := loadShipped(t, "poc")
	fx.Access.ServiceAccount.Token = testToken
	h, err := New(fx, WithLogger(slog.New(slog.DiscardHandler)), WithSubscriptionIDs(func(context.Context) ([]string, error) {
		return []string{"team-a-x7k2pq", "team-a", "team-ab-x7k2pq", "team-a-toolong7", "other-x7k2pq"}, nil
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)
	client, err := dynamic.NewForConfig(&rest.Config{Host: srv.URL, BearerToken: testToken})
	if err != nil {
		t.Fatalf("dynamic.NewForConfig: %v", err)
	}
	apps := client.Resource(testApplicationsGVR).Namespace("openshift-gitops")
	list, err := apps.List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	reported := map[string]map[string]any{}
	var holder string
	for _, app := range list.Items {
		resources, _, _ := unstructured.NestedSlice(app.Object, "status", "resources")
		for _, raw := range resources {
			res := raw.(map[string]any)
			if kind := res["kind"].(string); kind == "MaaSSubscription" || kind == "MaaSAuthPolicy" {
				reported[kind+"/"+res["name"].(string)] = res
				holder = app.GetName()
			}
		}
	}
	for _, key := range []string{"MaaSSubscription/team-a-x7k2pq", "MaaSAuthPolicy/team-a-x7k2pq-policy"} {
		res, ok := reported[key]
		if !ok {
			t.Fatalf("%s not reported; reported %v", key, reported)
		}
		if res["status"] != "Synced" || res["health"].(map[string]any)["status"] != "Healthy" {
			t.Errorf("%s = %v, want the fixture's Synced and Healthy", key, res)
		}
	}
	for _, key := range []string{"MaaSSubscription/team-ab-x7k2pq", "MaaSSubscription/team-a-toolong7", "MaaSSubscription/other-x7k2pq", "MaaSAuthPolicy/team-a-policy-policy"} {
		if _, ok := reported[key]; ok {
			t.Errorf("%s reported, want only base-<six characters>", key)
		}
	}
	// Get reports them too, and the stored fixture is left as it was.
	got, err := apps.Get(context.Background(), holder, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if resources, _, _ := unstructured.NestedSlice(got.Object, "status", "resources"); len(resources) != 4 {
		t.Errorf("Get() resources = %d, want the fixture's 2 and 2 generated", len(resources))
	}
	if resources, _, _ := unstructured.NestedSlice(h.applications["openshift-gitops"][holder].Object, "status", "resources"); len(resources) != 2 {
		t.Errorf("stored fixture resources = %d, want 2", len(resources))
	}
}

// Without WithSubscriptionIDs, or when the catalog can't be read, the
// Applications are the fixtures'.
func TestApplicationsWithoutCatalog(t *testing.T) {
	t.Parallel()
	fx := loadShipped(t, "poc")
	fx.Access.ServiceAccount.Token = testToken
	h, err := New(fx, WithLogger(slog.New(slog.DiscardHandler)), WithSubscriptionIDs(func(context.Context) ([]string, error) {
		return nil, errors.New("catalog unavailable")
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)
	client, err := dynamic.NewForConfig(&rest.Config{Host: srv.URL, BearerToken: testToken})
	if err != nil {
		t.Fatalf("dynamic.NewForConfig: %v", err)
	}
	list, err := client.Resource(testApplicationsGVR).Namespace("openshift-gitops").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, app := range list.Items {
		want, _, _ := unstructured.NestedSlice(h.applications["openshift-gitops"][app.GetName()].Object, "status", "resources")
		got, _, _ := unstructured.NestedSlice(app.Object, "status", "resources")
		if len(got) != len(want) {
			t.Errorf("%s resources = %d, want the fixture's %d", app.GetName(), len(got), len(want))
		}
	}
}
