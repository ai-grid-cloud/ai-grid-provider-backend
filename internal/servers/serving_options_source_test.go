package servers

import (
	"context"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"

	"github.com/osac-project/ai-grid-provider-backend/internal/gitops"

	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
)

const servingOptionsNamespace = "ai-grid"

func servingOptionsConfigMap(name string, data map[string]any) *unstructured.Unstructured {
	cm := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": name, "namespace": servingOptionsNamespace},
	}}
	if data != nil {
		cm.Object["data"] = data
	}
	return cm
}

// configMapSource serves the defaults testServingOptions and, when custom is
// not nil, a custom ConfigMap with that data.
func configMapSource(t *testing.T, custom map[string]any) (*ConfigMapServingOptions, *fake.FakeDynamicClient) {
	t.Helper()
	objects := []runtime.Object{servingOptionsConfigMap("defaults", map[string]any{ServingOptionsKey: testServingOptions})}
	if custom != nil {
		objects = append(objects, servingOptionsConfigMap("custom", custom))
	}
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), objects...)
	return &ConfigMapServingOptions{Client: client, Namespace: servingOptionsNamespace, Defaults: "defaults", Custom: "custom"}, client
}

func baseRefNames(o *ServingOptions) string {
	names := make([]string, 0, len(o.BaseRefs))
	for _, s := range o.BaseRefs {
		names = append(names, s.Name)
	}
	return strings.Join(names, ",")
}

// A custom entry replaces the default of the same name in place; any other is added after the defaults.
func TestConfigMapServingOptionsMerge(t *testing.T) {
	t.Parallel()
	source, _ := configMapSource(t, map[string]any{ServingOptionsKey: `
baseRefs:
- name: v3-6-0-kserve-config-llm-template-cpu
  displayName: vLLM CPU (ours)
- name: v3-6-0-kserve-config-llm-template-intel-gaudi
  displayName: vLLM Intel Gaudi
  accelerators: [habana.ai/gaudi]
`})
	got, err := source.load(context.Background())
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if names := baseRefNames(got); names != cpuPreset+","+gpuPreset+",v3-6-0-kserve-config-llm-template-intel-gaudi" {
		t.Fatalf("base refs = %s", names)
	}
	if s := got.BaseRefs[0]; s.DisplayName != "vLLM CPU (ours)" || s.Description != "" {
		t.Fatalf("replaced entry = %+v, want the custom one whole", s)
	}
}

// A missing custom ConfigMap means the defaults; one the backend can't use is ignored.
func TestConfigMapServingOptionsCustomProblems(t *testing.T) {
	t.Parallel()
	for name, custom := range map[string]map[string]any{
		"missing":       nil,
		"not YAML":      {ServingOptionsKey: "baseRefs: [oops"},
		"invalid entry": {ServingOptionsKey: "baseRefs:\n- name: Not_A_Name\n"},
		"no key":        {"other.yaml": "baseRefs: []"},
		"no data":       {},
	} {
		source, _ := configMapSource(t, custom)
		if custom != nil && len(custom) == 0 {
			source.Client = fake.NewSimpleDynamicClient(runtime.NewScheme(),
				servingOptionsConfigMap("defaults", map[string]any{ServingOptionsKey: testServingOptions}),
				servingOptionsConfigMap("custom", nil))
		}
		got, err := source.load(context.Background())
		if err != nil {
			t.Fatalf("%s: load() error = %v, want the defaults", name, err)
		}
		if names := baseRefNames(got); names != cpuPreset+","+gpuPreset {
			t.Fatalf("%s: base refs = %s, want the defaults", name, names)
		}
	}
}

func TestConfigMapServingOptionsDefaultsProblems(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	missing := &ConfigMapServingOptions{Client: fake.NewSimpleDynamicClient(runtime.NewScheme()),
		Namespace: servingOptionsNamespace, Defaults: "defaults"}
	_, err := missing.load(ctx)
	assertConnectCode(t, err, connect.CodeFailedPrecondition)

	invalid := &ConfigMapServingOptions{Namespace: servingOptionsNamespace, Defaults: "defaults",
		Client: fake.NewSimpleDynamicClient(runtime.NewScheme(),
			servingOptionsConfigMap("defaults", map[string]any{ServingOptionsKey: "baseRefs:\n- name: a\n- name: a\n"}))}
	_, err = invalid.load(ctx)
	assertConnectCode(t, err, connect.CodeInternal)
}

// Each call reads the ConfigMaps again, so an admin's edit applies without a
// restart, and Create checks against the list as it is now.
func TestConfigMapServingOptionsReadOnEveryCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source, client := configMapSource(t, nil)
	srv, err := NewModelsServer().SetWriter(gitops.NewMemoryWriter(nil)).SetServingOptionsSource(source).Build()
	if err != nil {
		t.Fatal(err)
	}
	gaudi := "v3-6-0-kserve-config-llm-template-intel-gaudi"
	model := func(id string) *aigridv1.Model {
		m := gitSampleModel(id, "site-a")
		m.Spec.BaseRefs = []string{gaudi}
		return m
	}
	_, err = srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: model("before")})
	assertConnectCode(t, err, connect.CodeInvalidArgument)

	added := servingOptionsConfigMap("custom", map[string]any{ServingOptionsKey: "baseRefs:\n- {name: " + gaudi + ", accelerators: [habana.ai/gaudi]}\n"})
	if _, err := client.Resource(configMapsResource).Namespace(servingOptionsNamespace).Create(ctx, added, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	resp, err := srv.ListServingOptions(ctx, &aigridv1.ModelsListServingOptionsRequest{})
	if err != nil || len(resp.GetBaseRefs()) != 3 || resp.GetBaseRefs()[2].GetName() != gaudi {
		t.Fatalf("ListServingOptions(after adding Gaudi) = %v, %v", resp, err)
	}
	if _, err := srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: model("after")}); err != nil {
		t.Fatalf("Create(Gaudi, now listed) error = %v", err)
	}

	broken := servingOptionsConfigMap("custom", map[string]any{ServingOptionsKey: "baseRefs: [oops"})
	if _, err := client.Resource(configMapsResource).Namespace(servingOptionsNamespace).Update(ctx, broken, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	resp, err = srv.ListServingOptions(ctx, &aigridv1.ModelsListServingOptionsRequest{})
	if err != nil || len(resp.GetBaseRefs()) != 2 {
		t.Fatalf("ListServingOptions(broken custom) = %v, %v, want the defaults", resp, err)
	}
}
