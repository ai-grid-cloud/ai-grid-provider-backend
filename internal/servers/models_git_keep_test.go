package servers

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"testing"

	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
	"github.com/osac-project/ai-grid-provider-backend/internal/gitops"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestModelsGitUpdateKeepsUnmodeledFields checks that Update keeps what a
// person added to the LLMInferenceService in Git that the API doesn't model
// (a volume, a second container, the main container's mounts and args, other
// spec fields), and still replaces or removes the fields the API owns.
func TestModelsGitUpdateKeepsUnmodeledFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer := gitops.NewMemoryWriter(nil)
	srv := mustGitModelsServer(t, writer)
	model := gitSampleModel("keep", "site-a")
	model.Spec.BaseRefs = []string{"preset-a"}
	model.Spec.Main.Requests = map[string]string{"cpu": "2"}
	if _, err := srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: model}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// A hand edit in Git.
	file := path.Join(modelDeploymentGitDir("keep"), "deployment.yaml")
	docs := readGitYAMLDocs(t, writer, file)
	llm := docs[0]
	mounts := []interface{}{map[string]interface{}{"name": "kv-cache", "mountPath": "/kv-cache"}}
	evictor := map[string]interface{}{"name": "pvc-evictor", "volumeMounts": mounts}
	containers, _, _ := unstructured.NestedSlice(llm.Object, "spec", "template", "containers")
	main := containers[0].(map[string]interface{})
	main["volumeMounts"] = mounts
	main["args"] = []interface{}{"--max-model-len=2048"}
	must(t, unstructured.SetNestedSlice(llm.Object, []interface{}{main, evictor}, "spec", "template", "containers"))
	volumes := []interface{}{map[string]interface{}{"name": "kv-cache", "emptyDir": map[string]interface{}{}}}
	must(t, unstructured.SetNestedSlice(llm.Object, volumes, "spec", "template", "volumes"))
	must(t, unstructured.SetNestedStringMap(llm.Object, map[string]string{"gpu": "true"}, "spec", "template", "nodeSelector"))
	must(t, unstructured.SetNestedField(llm.Object, int64(2), "spec", "parallelism", "tensor"))
	data, err := marshalYAMLDocs(docs...)
	must(t, err)
	head, err := writer.Head(ctx)
	must(t, err)
	if _, err := writer.Commit(ctx, head.Revision(), gitops.Change{Put: map[string][]byte{file: data}, Message: "hand edit"}); err != nil {
		t.Fatalf("hand edit commit: %v", err)
	}

	update := func(op string, object *aigridv1.Model, paths ...string) {
		t.Helper()
		if _, err := srv.Update(ctx, &aigridv1.ModelsUpdateRequest{Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: paths}}); err != nil {
			t.Fatalf("%s error = %v", op, err)
		}
	}
	object := gitSampleModel("keep", "site-a")
	object.Spec.Main.Requests = map[string]string{"cpu": "3"}
	update("Update(requests)", object, "spec.main.requests")
	update("Update(base_refs: none)", gitSampleModel("keep", "site-a"), "spec.base_refs")

	got := readGitYAMLDocs(t, writer, file)[0].Object
	wantMain := map[string]interface{}{
		"name":         "main",
		"image":        "quay.io/example/vllm:latest",
		"resources":    map[string]interface{}{"requests": map[string]interface{}{"cpu": "3"}},
		"volumeMounts": mounts,
		"args":         []interface{}{"--max-model-len=2048"},
	}
	gotContainers, _, _ := unstructured.NestedSlice(got, "spec", "template", "containers")
	if want := []interface{}{wantMain, evictor}; !reflect.DeepEqual(gotContainers, want) {
		t.Errorf("containers = %v, want %v", gotContainers, want)
	}
	if v, _, _ := unstructured.NestedSlice(got, "spec", "template", "volumes"); !reflect.DeepEqual(v, volumes) {
		t.Errorf("volumes = %v, want %v", v, volumes)
	}
	if v, _, _ := unstructured.NestedStringMap(got, "spec", "template", "nodeSelector"); !reflect.DeepEqual(v, map[string]string{"gpu": "true"}) {
		t.Errorf("nodeSelector = %v", v)
	}
	// YAML reads the number back as a float64.
	if v, _, _ := unstructured.NestedFieldNoCopy(got, "spec", "parallelism", "tensor"); fmt.Sprint(v) != "2" {
		t.Errorf("parallelism.tensor = %v, want 2", v)
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(got, "spec", "baseRefs"); found {
		t.Errorf("baseRefs kept after Update cleared them")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
