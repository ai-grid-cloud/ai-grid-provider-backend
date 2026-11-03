package servers

import (
	"context"
	"os"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"sigs.k8s.io/yaml"

	"github.com/osac-project/ai-grid-provider-backend/internal/gitops"

	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
)

const (
	cpuPreset = "v3-6-0-kserve-config-llm-template-cpu"
	gpuPreset = "v3-6-0-kserve-config-llm-single-node-template-nvidia-cuda"
)

// testServingOptions is the shape the console's chart renders.
const testServingOptions = `
baseRefs:
- name: v3-6-0-kserve-config-llm-template-cpu
  displayName: vLLM CPU
  description: Runs on CPUs.
  accelerators: []
- name: v3-6-0-kserve-config-llm-single-node-template-nvidia-cuda
  displayName: vLLM NVIDIA CUDA GPU, single node
  accelerators: [nvidia.com/gpu]
`

func mustServingOptions(t *testing.T, content string) *ServingOptions {
	t.Helper()
	opts, err := parseServingOptions([]byte(content))
	if err != nil {
		t.Fatalf("parseServingOptions() error = %v", err)
	}
	return opts
}

func servingOptionsServer(t *testing.T, writer gitops.Writer, opts *ServingOptions) *ModelsServer {
	t.Helper()
	srv, err := NewModelsServer().SetWriter(writer).SetServingOptions(opts).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return srv
}

func TestParseServingOptions(t *testing.T) {
	t.Parallel()
	got := mustServingOptions(t, testServingOptions).toProto()
	if len(got.GetBaseRefs()) != 2 || got.GetBaseRefs()[1].GetAccelerators()[0] != "nvidia.com/gpu" ||
		got.GetBaseRefs()[0].GetDisplayName() != "vLLM CPU" || got.GetBaseRefs()[0].GetDescription() != "Runs on CPUs." {
		t.Fatalf("base refs = %v", got.GetBaseRefs())
	}
	for name, content := range map[string]string{
		"unknown field": "baseRefs:\n- name: a\n  accelerator: nvidia.com/gpu\n",
		"named twice":   "baseRefs:\n- name: a\n- name: a\n",
		"bad name":      "baseRefs:\n- name: Not_A_Name\n",
		"no name":       "baseRefs:\n- displayName: A\n",
		"not YAML":      "baseRefs: [oops",
	} {
		if _, err := parseServingOptions([]byte(content)); err == nil {
			t.Errorf("parseServingOptions(%s) error = nil, want an error", name)
		}
	}
}

func TestListServingOptions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	resp, err := servingOptionsServer(t, gitops.NewMemoryWriter(nil), mustServingOptions(t, testServingOptions)).
		ListServingOptions(ctx, &aigridv1.ModelsListServingOptionsRequest{})
	if err != nil || len(resp.GetBaseRefs()) != 2 {
		t.Fatalf("ListServingOptions() = %v, %v", resp, err)
	}
	empty, err := servingOptionsServer(t, gitops.NewMemoryWriter(nil), nil).
		ListServingOptions(ctx, &aigridv1.ModelsListServingOptionsRequest{})
	if err != nil || len(empty.GetBaseRefs()) != 0 {
		t.Fatalf("ListServingOptions(no list) = %v, %v, want empty", empty, err)
	}
}

func TestModelsServingOptionsChecks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	withRefs := func(id string, refs ...string) *aigridv1.Model {
		m := gitSampleModel(id, "site-a")
		m.Spec.BaseRefs = refs
		return m
	}
	srv := servingOptionsServer(t, gitops.NewMemoryWriter(nil), mustServingOptions(t, testServingOptions))
	_, err := srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: withRefs("a", "v3-5-0-kserve-config-llm-template-cpu")})
	assertConnectCode(t, err, connect.CodeInvalidArgument)
	if !strings.Contains(err.Error(), "spec.base_refs[0]") {
		t.Errorf("Create(unlisted preset) error = %q, want the field named", err)
	}
	for _, m := range []*aigridv1.Model{withRefs("ok-cpu", cpuPreset), withRefs("ok-gpu", gpuPreset), withRefs("ok-none")} {
		if _, err := srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: m}); err != nil {
			t.Errorf("Create(%s) error = %v", m.GetId(), err)
		}
	}
	// Without a list: any preset.
	bare := servingOptionsServer(t, gitops.NewMemoryWriter(nil), nil)
	if _, err := bare.Create(ctx, &aigridv1.ModelsCreateRequest{Object: withRefs("any", "custom-preset")}); err != nil {
		t.Errorf("Create(no list, any preset) error = %v", err)
	}
}

// A model keeps a preset the list no longer has, as after an OpenShift AI
// upgrade renames the presets, but can't move to another unlisted one.
func TestModelsServingOptionsKeepsTheModelsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer := gitops.NewMemoryWriter(nil)
	old := servingOptionsServer(t, writer, mustServingOptions(t, testServingOptions))
	m := gitSampleModel("kept", "site-a")
	m.Spec.BaseRefs = []string{cpuPreset}
	if _, err := old.Create(ctx, &aigridv1.ModelsCreateRequest{Object: m}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	upgraded := servingOptionsServer(t, writer, mustServingOptions(t, strings.ReplaceAll(testServingOptions, "v3-6-0-", "v3-7-0-")))
	got, err := upgraded.Get(ctx, &aigridv1.ModelsGetRequest{Id: "kept"})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	edit := got.GetObject()
	replicas := int32(2)
	edit.Spec.Replicas = &replicas
	updated, err := upgraded.Update(ctx, &aigridv1.ModelsUpdateRequest{Object: edit,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.replicas", "spec.base_refs"}}})
	if err != nil {
		t.Fatalf("Update(keeping the model's own preset) error = %v", err)
	}
	edit = updated.GetObject()
	edit.Spec.BaseRefs = []string{"v3-5-0-kserve-config-llm-template-cpu"}
	_, err = upgraded.Update(ctx, &aigridv1.ModelsUpdateRequest{Object: edit,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.base_refs"}}})
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

// make dev-fake's defaults ConfigMap (hack/dev-fake.sh) is one the backend accepts.
func TestDevFakeServingOptionsLoad(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../fixtures/configmaps/ai-grid/ai-grid-serving-options.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if err := yaml.Unmarshal(raw, &cm); err != nil {
		t.Fatal(err)
	}
	opts, err := parseServingOptions([]byte(cm.Data[ServingOptionsKey]))
	if err != nil {
		t.Fatalf("parseServingOptions(fixture) error = %v", err)
	}
	if !opts.baseRef(cpuPreset) || !opts.baseRef(gpuPreset) || !opts.baseRef("v3-6-0-kserve-config-llm-template-amd-rocm") {
		t.Fatalf("base refs = %v, want the CPU, NVIDIA single-node and AMD presets", opts.BaseRefs)
	}
}
