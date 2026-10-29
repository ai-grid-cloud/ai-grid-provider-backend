package servers

import (
	"context"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"
	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
	"github.com/osac-project/ai-grid-provider-backend/internal/gitops"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// The ingress site's AI gateway names a Service after each model's id
// (provider-selection-required-<id>), and one name over Kubernetes' 63
// characters fails its reconcile for every model on that gateway.
func TestModelsGitCreateRefusesIDLongerThan35(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := mustGitModelsServer(t, gitops.NewMemoryWriter(nil))

	fits := strings.Repeat("a", 35)
	if _, err := srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: gitSampleModel(fits, "site-a")}); err != nil {
		t.Fatalf("Create(35 characters) error = %v", err)
	}

	// demo1's modelcar-granite-4-0-h-tiny-fp8-dynamic, 39 characters.
	for _, id := range []string{strings.Repeat("b", 36), "modelcar-granite-4-0-h-tiny-fp8-dynamic"} {
		_, err := srv.Create(ctx, &aigridv1.ModelsCreateRequest{Object: gitSampleModel(id, "site-a")})
		assertConnectCode(t, err, connect.CodeInvalidArgument)
		if msg := err.Error(); !strings.Contains(msg, "35 characters") || !strings.Contains(msg, "provider-selection-required-") {
			t.Errorf("Create(%d characters) error = %q, want it to name the 35-character limit and the gateway's Service name", len(id), msg)
		}
	}
}

// A model saved before the limit keeps working: the limit is on Create only.
func TestModelsGitLongIDSavedBeforeTheLimitStillWorks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := mustGitModelsServer(t, gitops.NewMemoryWriter(nil))

	id := "modelcar-granite-4-0-h-tiny-fp8-dynamic"
	if _, err := srv.gitCreate(ctx, gitSampleModel(id, "site-a")); err != nil {
		t.Fatalf("seed: gitCreate() error = %v", err)
	}
	got, err := srv.Get(ctx, &aigridv1.ModelsGetRequest{Id: id})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	replicas := int32(2)
	edit := &aigridv1.Model{
		Id:       id,
		Metadata: &aigridv1.Metadata{Name: id, Version: got.GetObject().GetMetadata().GetVersion()},
		Spec:     &aigridv1.ModelSpec{Replicas: &replicas},
	}
	updated, err := srv.Update(ctx, &aigridv1.ModelsUpdateRequest{Object: edit, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.replicas"}}})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := srv.Delete(ctx, &aigridv1.ModelsDeleteRequest{Id: id, ResourceVersion: updated.GetObject().GetMetadata().GetVersion()}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}
