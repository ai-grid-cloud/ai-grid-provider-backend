package servers

import (
	"context"
	"slices"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"
	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
	"github.com/osac-project/ai-grid-provider-backend/internal/gitops"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

const testOrgID = "0b6c2f1e-org"

func TestSubscriptionsGitCRUD(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer := gitops.NewMemoryWriter(nil)
	srv := mustGitSubscriptionsServer(t, writer)

	created, err := srv.Create(ctx, &aigridv1.SubscriptionsCreateRequest{Object: gitSampleSubscription("team-a")})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got := created.GetObject(); got.GetId() != "team-a" || got.GetMetadata().GetVersion() != 1 {
		t.Fatalf("Create object id = %q, version = %d", got.GetId(), got.GetMetadata().GetVersion())
	}
	if got := created.GetObject().GetSpec().GetOwner().GetGroups(); !slices.Equal(got, []string{"team-a"}) {
		t.Fatalf("Create owner.groups = %v, want the bare name", got)
	}
	if _, ok := created.GetObject().GetMetadata().GetAnnotations()[gitVersionAnnotation]; ok {
		t.Fatal("Create returned the version annotation")
	}
	if created.GetAuthPolicy().GetId() != "team-a-policy" {
		t.Fatalf("Create auth_policy = %v", created.GetAuthPolicy())
	}

	sub, policy := readGitSubscriptionFile(t, writer, "team-a")
	if sub.GetNamespace() != "models-as-a-service" || sub.GetAnnotations()[gitVersionAnnotation] != "1" {
		t.Fatalf("file MaaSSubscription namespace = %q, annotations = %v", sub.GetNamespace(), sub.GetAnnotations())
	}
	if got := groupNames(t, sub, "spec", "owner", "groups"); !slices.Equal(got, []string{testOrgID + "/team-a"}) {
		t.Fatalf("file owner.groups = %v", got)
	}
	if got := groupNames(t, policy, "spec", "subjects", "groups"); !slices.Equal(got, []string{testOrgID + "/team-a"}) {
		t.Fatalf("file policy subjects.groups = %v", got)
	}
	if policy.GetName() != "team-a-policy" || policy.GetLabels()[managedByLabelKey] != managedByLabelValue {
		t.Fatalf("file policy name = %q, labels = %v", policy.GetName(), policy.GetLabels())
	}

	got, err := srv.Get(ctx, &aigridv1.SubscriptionsGetRequest{Id: "team-a"})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.GetObject().GetSpec().GetPriority() != 10 || len(got.GetAuthPolicies()) != 1 {
		t.Fatalf("Get priority = %d, auth_policies = %v", got.GetObject().GetSpec().GetPriority(), got.GetAuthPolicies())
	}

	update := gitSampleSubscription("team-a")
	update.Metadata.Version = 1
	update.Spec.Priority = 20
	updated, err := srv.Update(ctx, &aigridv1.SubscriptionsUpdateRequest{
		Object:     update,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec"}},
		Lock:       true,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.GetObject().GetMetadata().GetVersion() != 2 || updated.GetObject().GetSpec().GetPriority() != 20 {
		t.Fatalf("Update version = %d, priority = %d", updated.GetObject().GetMetadata().GetVersion(), updated.GetObject().GetSpec().GetPriority())
	}

	_, err = srv.Update(ctx, &aigridv1.SubscriptionsUpdateRequest{Object: update, Lock: true})
	assertConnectCode(t, err, connect.CodeAborted)

	list, err := srv.List(ctx, &aigridv1.SubscriptionsListRequest{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if list.GetTotal() != 1 || list.GetItems()[0].GetMetadata().GetVersion() != 2 {
		t.Fatalf("List = %v", list)
	}

	if _, err := srv.Delete(ctx, &aigridv1.SubscriptionsDeleteRequest{Id: "team-a"}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	_, err = srv.Get(ctx, &aigridv1.SubscriptionsGetRequest{Id: "team-a"})
	assertConnectCode(t, err, connect.CodeNotFound)

	want := []string{"subscriptions: create team-a", "subscriptions: update team-a", "subscriptions: delete team-a"}
	if got := writer.Messages(); !slices.Equal(got, want) {
		t.Fatalf("commit messages = %v, want %v", got, want)
	}
}

func TestSubscriptionsGitUpdateMasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer := gitops.NewMemoryWriter(nil)
	srv := mustGitSubscriptionsServer(t, writer)
	if _, err := srv.Create(ctx, &aigridv1.SubscriptionsCreateRequest{Object: gitSampleSubscription("team-a")}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	labelsOnly := &aigridv1.Subscription{Id: "team-a", Metadata: &aigridv1.Metadata{Labels: map[string]string{"tier": "gold"}}}
	updated, err := srv.Update(ctx, &aigridv1.SubscriptionsUpdateRequest{
		Object:     labelsOnly,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
	})
	if err != nil {
		t.Fatalf("Update(labels) error = %v", err)
	}
	if updated.GetObject().GetMetadata().GetLabels()["tier"] != "gold" || updated.GetObject().GetSpec().GetPriority() != 10 {
		t.Fatalf("Update(labels) = %v", updated.GetObject())
	}
	if updated.GetObject().GetMetadata().GetAnnotations()["openshift.io/display-name"] != "team-a" {
		t.Fatalf("Update(labels) dropped annotations: %v", updated.GetObject().GetMetadata().GetAnnotations())
	}

	// A mask that names nothing the server stores changes nothing and commits nothing.
	if _, err := srv.Update(ctx, &aigridv1.SubscriptionsUpdateRequest{
		Object:     labelsOnly,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
	}); err != nil {
		t.Fatalf("Update(status) error = %v", err)
	}
	if n := len(writer.Messages()); n != 2 {
		t.Fatalf("commits = %d, want 2", n)
	}

	_, err = srv.Update(ctx, &aigridv1.SubscriptionsUpdateRequest{Object: labelsOnly, Lock: true})
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestSubscriptionsGitValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := mustGitSubscriptionsServer(t, gitops.NewMemoryWriter(nil))

	noOrg := gitSampleSubscription("team-a")
	noOrg.Spec.Organization = nil
	_, err := srv.Create(ctx, &aigridv1.SubscriptionsCreateRequest{Object: noOrg})
	assertConnectCode(t, err, connect.CodeInvalidArgument)

	for _, org := range []string{"org/admins", "Org A", "org\n"} {
		badOrg := gitSampleSubscription("team-a")
		badOrg.Spec.Organization.Id = org
		_, err = srv.Create(ctx, &aigridv1.SubscriptionsCreateRequest{Object: badOrg})
		assertConnectCode(t, err, connect.CodeInvalidArgument)
	}

	qualified := gitSampleSubscription("team-a")
	qualified.Spec.Owner.Groups = []string{testOrgID + "/team-a"}
	_, err = srv.Create(ctx, &aigridv1.SubscriptionsCreateRequest{Object: qualified})
	assertConnectCode(t, err, connect.CodeInvalidArgument)

	if _, err := srv.Create(ctx, &aigridv1.SubscriptionsCreateRequest{Object: gitSampleSubscription("team-a")}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = srv.Create(ctx, &aigridv1.SubscriptionsCreateRequest{Object: gitSampleSubscription("team-a")})
	assertConnectCode(t, err, connect.CodeAlreadyExists)

	otherOrg := gitSampleSubscription("team-a")
	otherOrg.Spec.Organization.Id = "other-org"
	_, err = srv.Update(ctx, &aigridv1.SubscriptionsUpdateRequest{Object: otherOrg})
	assertConnectCode(t, err, connect.CodeInvalidArgument)

	_, err = srv.Update(ctx, &aigridv1.SubscriptionsUpdateRequest{Object: gitSampleSubscription("missing")})
	assertConnectCode(t, err, connect.CodeNotFound)
	_, err = srv.Delete(ctx, &aigridv1.SubscriptionsDeleteRequest{Id: "missing"})
	assertConnectCode(t, err, connect.CodeNotFound)
}

// TestSubscriptionsGitConcurrentWrite checks that a write whose base went
// stale between its read and its commit answers Aborted instead of
// overwriting the other write.
func TestSubscriptionsGitConcurrentWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer := &racingWriter{MemoryWriter: gitops.NewMemoryWriter(nil)}
	srv := mustGitSubscriptionsServer(t, writer)
	if _, err := srv.Create(ctx, &aigridv1.SubscriptionsCreateRequest{Object: gitSampleSubscription("team-a")}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	writer.race = func() {
		head, _ := writer.Head(ctx)
		if _, err := writer.MemoryWriter.Commit(ctx, head.Revision(), gitops.Change{
			Put:     map[string][]byte{subscriptionGitPath("team-a"): []byte("other write")},
			Message: "other",
		}); err != nil {
			t.Errorf("racing Commit() error = %v", err)
		}
	}
	update := gitSampleSubscription("team-a")
	update.Spec.Priority = 30
	_, err := srv.Update(ctx, &aigridv1.SubscriptionsUpdateRequest{Object: update})
	assertConnectCode(t, err, connect.CodeAborted)
}

func TestSubscriptionsGitListFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := mustGitSubscriptionsServer(t, gitops.NewMemoryWriter(map[string][]byte{
		subscriptionsGitDir + "/README.md": []byte("not a subscription"),
	}))
	for _, id := range []string{"team-a", "team-b"} {
		if _, err := srv.Create(ctx, &aigridv1.SubscriptionsCreateRequest{Object: gitSampleSubscription(id)}); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}
	list, err := srv.List(ctx, &aigridv1.SubscriptionsListRequest{Filter: ptrString(`this.id == "team-b"`)})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if list.GetTotal() != 1 || list.GetItems()[0].GetId() != "team-b" {
		t.Fatalf("List = %v", list)
	}
}

func TestSubscriptionsGitRejectsBrokenFile(t *testing.T) {
	t.Parallel()
	srv := mustGitSubscriptionsServer(t, gitops.NewMemoryWriter(map[string][]byte{
		subscriptionGitPath("team-a"): []byte("kind: MaaSSubscription\nmetadata:\n  name: team-a\n"),
	}))
	_, err := srv.Get(context.Background(), &aigridv1.SubscriptionsGetRequest{Id: "team-a"})
	assertConnectCode(t, err, connect.CodeInternal)
	_, err = srv.List(context.Background(), &aigridv1.SubscriptionsListRequest{})
	assertConnectCode(t, err, connect.CodeInternal)
}

// racingWriter runs race, once, between a Head and the Commit that follows it.
type racingWriter struct {
	*gitops.MemoryWriter
	race func()
}

func (w *racingWriter) Commit(ctx context.Context, base string, c gitops.Change) (string, error) {
	if race := w.race; race != nil {
		w.race = nil
		race()
	}
	return w.MemoryWriter.Commit(ctx, base, c)
}

func mustGitSubscriptionsServer(t *testing.T, writer gitops.Writer) *SubscriptionsServer {
	t.Helper()
	// No dynamic client: git mode must never call the cluster.
	srv, err := NewSubscriptionsServer().SetWriter(writer).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return srv
}

func gitSampleSubscription(id string) *aigridv1.Subscription {
	sub := sampleSubscription(id)
	sub.Spec.Organization = &aigridv1.SubscriptionOrganization{Id: testOrgID}
	return sub
}

func readGitSubscriptionFile(t *testing.T, writer gitops.Writer, id string) (sub, policy *unstructured.Unstructured) {
	t.Helper()
	head, err := writer.Head(context.Background())
	if err != nil {
		t.Fatalf("Head() error = %v", err)
	}
	data, err := head.Read(subscriptionGitPath(id))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	for _, part := range strings.Split(string(data), "\n---\n") {
		obj := map[string]interface{}{}
		if err := yaml.Unmarshal([]byte(part), &obj); err != nil {
			t.Fatalf("unmarshal %q: %v", part, err)
		}
		item := &unstructured.Unstructured{Object: obj}
		switch item.GetKind() {
		case "MaaSSubscription":
			sub = item
		case "MaaSAuthPolicy":
			policy = item
		}
	}
	if sub == nil || policy == nil {
		t.Fatalf("file %s lacks a MaaSSubscription or MaaSAuthPolicy:\n%s", subscriptionGitPath(id), data)
	}
	return sub, policy
}

func groupNames(t *testing.T, obj *unstructured.Unstructured, fields ...string) []string {
	t.Helper()
	groups, _, err := unstructured.NestedSlice(obj.Object, fields...)
	if err != nil {
		t.Fatalf("%v: %v", fields, err)
	}
	var out []string
	for _, g := range groups {
		out = append(out, g.(map[string]interface{})["name"].(string))
	}
	return out
}

// A model ref without a namespace points at the catalog entry in the
// subscription's own namespace, where git mode writes MaaSModelRefs, not at
// cluster mode's "llm" default.
func TestSubscriptionsGitModelRefNamespaceDefault(t *testing.T) {
	t.Parallel()
	writer := gitops.NewMemoryWriter(nil)
	srv := mustGitSubscriptionsServer(t, writer)

	object := gitSampleSubscription("team-a")
	object.Spec.ModelRefs[0].Namespace = ""
	if _, err := srv.Create(context.Background(), &aigridv1.SubscriptionsCreateRequest{Object: object}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	sub, policy := readGitSubscriptionFile(t, writer, "team-a")
	for _, obj := range []*unstructured.Unstructured{sub, policy} {
		refs, _, _ := unstructured.NestedSlice(obj.Object, "spec", "modelRefs")
		if len(refs) != 1 || refs[0].(map[string]interface{})["namespace"] != "models-as-a-service" {
			t.Fatalf("file %s modelRefs = %v, want namespace models-as-a-service", obj.GetKind(), refs)
		}
	}
}
