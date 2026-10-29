package servers

import (
	"context"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"
	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
	"github.com/osac-project/ai-grid-provider-backend/internal/gitops"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// MaaS lists a model under its id and forwards the request body unchanged, so
// a tenant's "model" is the id all the way to vLLM. The backend serves the
// model as its id (the LLMInferenceService's spec.model.name, vLLM's served
// name) and points the ExternalModel's targetModel at the id. As in
// odh-dashboard, the served name is the resource name: spec.model.name is
// accepted empty or equal to the id and always reads back as the id. Two
// models may serve one Hugging Face model on one cluster: their ids differ.
func TestModelsGitServesModelByID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer := gitops.NewMemoryWriter(nil)
	srv := mustGitModelsServer(t, writer)

	for id, sent := range map[string]string{"first": "", "second": "second"} {
		model := gitSampleModel(id, "site-b")
		model.Spec.Model.Name = sent
		created, err := srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: model})
		if err != nil {
			t.Fatalf("Create(%s, spec.model.name %q) error = %v", id, sent, err)
		}
		if name := created.GetObject().GetSpec().GetModel().GetName(); name != id {
			t.Fatalf("%s: Create spec.model.name = %q, want the id", id, name)
		}
		llm := readGitYAMLDocs(t, writer, "catalog/models/"+id+"/deployment/deployment.yaml")[0]
		if got, _, _ := unstructured.NestedString(llm.Object, "spec", "model", "name"); got != id {
			t.Fatalf("%s: LLMInferenceService spec.model.name = %q, want the id (vLLM's served name)", id, got)
		}
		if _, ok := llm.GetAnnotations()["ai-grid/model-name"]; ok {
			t.Fatalf("%s: LLMInferenceService has annotation ai-grid/model-name; the backend no longer writes it", id)
		}
		if got := gitTargetModel(t, writer, id); got != id {
			t.Fatalf("%s: ExternalModel targetModel = %q, want the id", id, got)
		}
		got, err := srv.Get(ctx, &aigridv1.ModelsGetRequest{Id: id})
		if err != nil {
			t.Fatalf("Get(%s) error = %v", id, err)
		}
		if name := got.GetObject().GetSpec().GetModel().GetName(); name != id {
			t.Fatalf("%s: Get spec.model.name = %q, want the id", id, name)
		}
	}
}

// A spec.model.name other than the id would be a name nothing serves, so
// Create and Update refuse it, saying the model is served as its id.
func TestModelsGitRejectsServedNameOtherThanID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer := gitops.NewMemoryWriter(nil)
	srv := mustGitModelsServer(t, writer)

	model := gitSampleModel("opt", "site-a")
	model.Spec.Model.Name = "facebook/opt-125m"
	_, err := srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: model})
	assertConnectCode(t, err, connect.CodeInvalidArgument)
	if msg := err.Error(); !strings.Contains(msg, `"facebook/opt-125m"`) || !strings.Contains(msg, `"opt"`) {
		t.Fatalf("Create error = %v, want it to name the sent name and the id", err)
	}

	created, err := srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: gitSampleModel("opt", "site-a")})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, path := range []string{"spec.model", "spec.model.name"} {
		edit := gitSampleModel("opt", "site-a")
		edit.Spec.Model.Name = "renamed"
		edit.Metadata.Version = created.GetObject().GetMetadata().GetVersion()
		_, err = srv.Update(ctx, &aigridv1.ModelsUpdateRequest{
			Object:     edit,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
			Lock:       true,
		})
		assertConnectCode(t, err, connect.CodeInvalidArgument)
	}
}

// A model written before the backend served models as their id serves its
// own spec.model.name, such as a Hugging Face name, and the first builds that
// served the id kept the caller's name in annotation ai-grid/model-name.
// Either reads back with spec.model.name the id and no annotation, and its
// next update rewrites it to serve the id (vLLM restarts once) without the
// annotation.
func TestModelsGitMigratesEntryServingAnotherName(t *testing.T) {
	t.Parallel()
	for name, annotate := range map[string]bool{"before ids": false, "with ai-grid/model-name": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			writer := gitops.NewMemoryWriter(nil)
			srv := mustGitModelsServer(t, writer)

			if _, err := srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: gitSampleModel("old", "site-a")}); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			// Rewrite the deployment the way an earlier backend wrote it.
			p := "catalog/models/old/deployment/deployment.yaml"
			docs := readGitYAMLDocs(t, writer, p)
			if annotate {
				annotations := docs[0].GetAnnotations()
				annotations["ai-grid/model-name"] = "facebook/opt-125m"
				docs[0].SetAnnotations(annotations)
			}
			if err := unstructured.SetNestedField(docs[0].Object, "facebook/opt-125m", "spec", "model", "name"); err != nil {
				t.Fatal(err)
			}
			data, err := marshalYAMLDocs(docs...)
			if err != nil {
				t.Fatal(err)
			}
			head, err := writer.Head(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Commit(ctx, head.Revision(), gitops.Change{Put: map[string][]byte{p: data}, Message: "old format"}); err != nil {
				t.Fatalf("Commit() error = %v", err)
			}

			got, err := srv.Get(ctx, &aigridv1.ModelsGetRequest{Id: "old"})
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if name := got.GetObject().GetSpec().GetModel().GetName(); name != "old" {
				t.Fatalf("Get spec.model.name = %q, want the id", name)
			}
			if _, ok := got.GetObject().GetMetadata().GetAnnotations()["ai-grid/model-name"]; ok {
				t.Fatal("Get returned annotation ai-grid/model-name")
			}

			replicas := int32(2)
			edit := gitSampleModel("old", "site-a")
			edit.Spec.Replicas = &replicas
			edit.Metadata.Version = got.GetObject().GetMetadata().GetVersion()
			if _, err := srv.Update(ctx, &aigridv1.ModelsUpdateRequest{
				Object:     edit,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.replicas"}},
				Lock:       true,
			}); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			llm := readGitYAMLDocs(t, writer, p)[0]
			if served, _, _ := unstructured.NestedString(llm.Object, "spec", "model", "name"); served != "old" {
				t.Fatalf("after update spec.model.name = %q, want the id", served)
			}
			if _, ok := llm.GetAnnotations()["ai-grid/model-name"]; ok {
				t.Fatal("after update the deployment still has annotation ai-grid/model-name")
			}
		})
	}
}

// Annotation ai-grid/api picks the OpenAI API the ExternalModel calls:
// completions for base models with no chat template, chat (the default)
// otherwise.
func TestModelsGitAPIAnnotation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer := gitops.NewMemoryWriter(nil)
	srv := mustGitModelsServer(t, writer)

	for id, want := range map[string]string{"chat-default": "/v1/chat/completions", "chat": "/v1/chat/completions", "completions": "/v1/completions"} {
		model := gitSampleModel(id, "site-"+id)
		if id != "chat-default" {
			model.Metadata.Annotations["ai-grid/api"] = id
		}
		created, err := srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: model})
		if err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
		external := readGitYAMLDocs(t, writer, "catalog/models/"+id+"/catalog/model.yaml")[0]
		refs, _, _ := unstructured.NestedSlice(external.Object, "spec", "externalProviderRefs")
		if got := refs[0].(map[string]interface{})["path"]; got != want {
			t.Fatalf("%s: ExternalModel path = %v, want %s", id, got, want)
		}
		if id != "chat-default" && created.GetObject().GetMetadata().GetAnnotations()["ai-grid/api"] != id {
			t.Fatalf("%s: Create annotations = %v, want ai-grid/api kept", id, created.GetObject().GetMetadata().GetAnnotations())
		}
	}

	bad := gitSampleModel("bad", "site-a")
	bad.Metadata.Annotations["ai-grid/api"] = "embeddings"
	_, err := srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: bad})
	assertConnectCode(t, err, connect.CodeInvalidArgument)

	// Subscriptions still don't accept the annotation.
	if err := validateGitMetadata(&aigridv1.Metadata{Annotations: map[string]string{"ai-grid/api": "chat"}}); err == nil {
		t.Fatal("validateGitMetadata accepted ai-grid/api, want it limited to models")
	}
}

func gitTargetModel(t *testing.T, writer gitops.Writer, id string) string {
	t.Helper()
	external := readGitYAMLDocs(t, writer, "catalog/models/"+id+"/catalog/model.yaml")[0]
	refs, _, _ := unstructured.NestedSlice(external.Object, "spec", "externalProviderRefs")
	if len(refs) != 1 {
		t.Fatalf("%s: ExternalModel externalProviderRefs = %v, want one", id, refs)
	}
	target, _ := refs[0].(map[string]interface{})["targetModel"].(string)
	return target
}
