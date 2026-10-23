package servers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	connect "connectrpc.com/connect/v2"
	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
	"github.com/osac-project/ai-grid-provider-backend/internal/gitops"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// In git mode each subscription is one file, catalog/subscriptions/<id>.yaml,
// holding its MaaSSubscription and its managed MaaSAuthPolicy <id>-policy.
// Argo CD applies the directory to the ingress clusters.
const (
	subscriptionsGitDir = "catalog/subscriptions"

	// gitVersionAnnotation counts the changes to a file. It stands in for the
	// resourceVersion that metadata.version maps to in cluster mode.
	gitVersionAnnotation = "ai-grid/version"
)

func subscriptionGitPath(id string) string {
	return path.Join(subscriptionsGitDir, id+".yaml")
}

// subscriptionDoc is a parsed subscription file.
type subscriptionDoc struct {
	sub    *aigridv1.Subscription
	policy *aigridv1.AuthPolicy
}

func (s *SubscriptionsServer) gitList(ctx context.Context) ([]*aigridv1.Subscription, error) {
	head, err := s.writer.Head(ctx)
	if err != nil {
		return nil, gitError("list", "", err)
	}
	names, err := head.List(subscriptionsGitDir)
	if err != nil {
		return nil, gitError("list", "", err)
	}
	items := make([]*aigridv1.Subscription, 0, len(names))
	for _, name := range names {
		id, ok := strings.CutSuffix(name, ".yaml")
		if !ok {
			continue
		}
		doc, err := readSubscriptionDoc(head, id)
		if err != nil {
			return nil, err
		}
		items = append(items, doc.sub)
	}
	return items, nil
}

func (s *SubscriptionsServer) gitGet(ctx context.Context, id string) (*aigridv1.SubscriptionsGetResponse, error) {
	head, err := s.writer.Head(ctx)
	if err != nil {
		return nil, gitError("get", id, err)
	}
	doc, err := readSubscriptionDoc(head, id)
	if err != nil {
		return nil, err
	}
	resp := &aigridv1.SubscriptionsGetResponse{Object: doc.sub}
	if doc.policy != nil {
		resp.AuthPolicies = []*aigridv1.AuthPolicy{doc.policy}
	}
	return resp, nil
}

func (s *SubscriptionsServer) gitCreate(ctx context.Context, object *aigridv1.Subscription) (*aigridv1.SubscriptionsCreateResponse, error) {
	id := subscriptionID(object)
	if err := validateGitOwner(object.GetSpec()); err != nil {
		return nil, err
	}
	head, err := s.writer.Head(ctx)
	if err != nil {
		return nil, gitError("create", id, err)
	}
	if _, err := head.Read(subscriptionGitPath(id)); err == nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "Subscription %q already exists", id)
	} else if !errors.Is(err, gitops.ErrNotFound) {
		return nil, gitError("create", id, err)
	}
	doc, err := s.gitCommit(ctx, head.Revision(), "create", object, 1)
	if err != nil {
		return nil, err
	}
	return &aigridv1.SubscriptionsCreateResponse{Object: doc.sub, AuthPolicy: doc.policy}, nil
}

func (s *SubscriptionsServer) gitUpdate(ctx context.Context, req *aigridv1.SubscriptionsUpdateRequest) (*aigridv1.SubscriptionsUpdateResponse, error) {
	object := req.GetObject()
	id := subscriptionID(object)
	head, err := s.writer.Head(ctx)
	if err != nil {
		return nil, gitError("update", id, err)
	}
	current, err := readSubscriptionDoc(head, id)
	if err != nil {
		return nil, err
	}

	version := object.GetMetadata().GetVersion()
	if req.GetLock() && version == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "lock requires metadata.version")
	}
	if version != 0 && version != current.sub.GetMetadata().GetVersion() {
		return nil, staleSubscriptionError("update", id)
	}

	next := proto.CloneOf(current.sub)
	next.Status = nil
	paths := subscriptionUpdatePaths(req.GetUpdateMask(), object)
	if paths.labels {
		next.Metadata.Labels = object.GetMetadata().GetLabels()
	}
	if paths.annotations {
		next.Metadata.Annotations = object.GetMetadata().GetAnnotations()
	}
	if paths.spec {
		if err := validateSubscriptionSpec(object.GetSpec()); err != nil {
			return nil, err
		}
		if err := validateOrganizationImmutable(current.sub.GetSpec(), object.GetSpec()); err != nil {
			return nil, err
		}
		if err := validateGitOwner(object.GetSpec()); err != nil {
			return nil, err
		}
		next.Spec = object.GetSpec()
	}
	if !paths.labels && !paths.annotations && !paths.spec {
		return &aigridv1.SubscriptionsUpdateResponse{Object: current.sub}, nil
	}

	doc, err := s.gitCommit(ctx, head.Revision(), "update", next, current.sub.GetMetadata().GetVersion()+1)
	if err != nil {
		return nil, err
	}
	return &aigridv1.SubscriptionsUpdateResponse{Object: doc.sub}, nil
}

func (s *SubscriptionsServer) gitDelete(ctx context.Context, id string) error {
	head, err := s.writer.Head(ctx)
	if err != nil {
		return gitError("delete", id, err)
	}
	if _, err := readSubscriptionDoc(head, id); err != nil {
		return err
	}
	_, err = s.writer.Commit(ctx, head.Revision(), gitops.Change{
		Delete:  []string{subscriptionGitPath(id)},
		Message: "subscriptions: delete " + id,
	})
	if err != nil {
		return gitError("delete", id, err)
	}
	return nil
}

// gitCommit writes object's file at version on top of base and returns the
// subscription as read back from that file.
func (s *SubscriptionsServer) gitCommit(ctx context.Context, base, op string, object *aigridv1.Subscription, version int32) (*subscriptionDoc, error) {
	id := subscriptionID(object)
	data, err := renderSubscriptionDoc(object, s.namespace, version)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "%s Subscription %q: %v", op, id, err).WithCause(err)
	}
	if _, err := s.writer.Commit(ctx, base, gitops.Change{
		Put:     map[string][]byte{subscriptionGitPath(id): data},
		Message: fmt.Sprintf("subscriptions: %s %s", op, id),
	}); err != nil {
		return nil, gitError(op, id, err)
	}
	doc, err := parseSubscriptionDoc(id, data)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "%s Subscription %q: %v", op, id, err).WithCause(err)
	}
	return doc, nil
}

// validateGitOwner checks what git mode adds to the owner: an organization, and
// bare group names. The backend writes each group as <organization id>/<group>,
// the tenant-qualified form the gateway puts in the token, so the organization
// id must not contain a separator that would change which group is granted.
func validateGitOwner(spec *aigridv1.SubscriptionSpec) error {
	org := spec.GetOrganization().GetId()
	if org == "" {
		return connect.NewError(connect.CodeInvalidArgument, "spec.organization.id is required")
	}
	if !subscriptionNameRE.MatchString(org) {
		return connect.Errorf(connect.CodeInvalidArgument, "spec.organization.id %q is not a valid organization id", org)
	}
	for i, group := range spec.GetOwner().GetGroups() {
		if group == "" || strings.Contains(group, "/") {
			return connect.Errorf(connect.CodeInvalidArgument, "spec.owner.groups[%d] %q must be a bare group name, without the organization", i, group)
		}
	}
	return nil
}

// renderSubscriptionDoc renders the subscription file. Owner groups get the
// organization prefix, and the version annotation is set on the subscription.
// A model ref without a namespace gets the subscription's namespace, where git
// mode writes each model's MaaSModelRef, instead of cluster mode's default.
func renderSubscriptionDoc(object *aigridv1.Subscription, namespace string, version int32) ([]byte, error) {
	qualified := proto.CloneOf(object)
	org := qualified.GetSpec().GetOrganization().GetId()
	if owner := qualified.GetSpec().GetOwner(); owner != nil {
		for i, group := range owner.Groups {
			owner.Groups[i] = org + "/" + group
		}
	}
	for _, ref := range qualified.GetSpec().GetModelRefs() {
		if ref.GetNamespace() == "" {
			ref.Namespace = namespace
		}
	}

	sub := subscriptionToUnstructured(qualified, namespace)
	annotations := sub.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[gitVersionAnnotation] = strconv.FormatInt(int64(version), 10)
	sub.SetAnnotations(annotations)

	var out bytes.Buffer
	for i, obj := range []*unstructured.Unstructured{sub, authPolicyFromSubscription(qualified, namespace)} {
		data, err := yaml.Marshal(obj.Object)
		if err != nil {
			return nil, fmt.Errorf("marshal %s: %w", obj.GetKind(), err)
		}
		if i > 0 {
			out.WriteString("---\n")
		}
		out.Write(data)
	}
	return out.Bytes(), nil
}

func readSubscriptionDoc(head gitops.Snapshot, id string) (*subscriptionDoc, error) {
	data, err := head.Read(subscriptionGitPath(id))
	if err != nil {
		return nil, gitError("get", id, err)
	}
	doc, err := parseSubscriptionDoc(id, data)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "read %s: %v", subscriptionGitPath(id), err).WithCause(err)
	}
	return doc, nil
}

// parseSubscriptionDoc reverses renderSubscriptionDoc: it strips the
// organization prefix from owner groups and moves the version annotation to
// metadata.version.
func parseSubscriptionDoc(id string, data []byte) (*subscriptionDoc, error) {
	doc := &subscriptionDoc{}
	for _, part := range bytes.Split(data, []byte("\n---\n")) {
		obj := map[string]interface{}{}
		if err := yaml.Unmarshal(part, &obj); err != nil {
			return nil, err
		}
		item := unstructured.Unstructured{Object: obj}
		switch item.GetKind() {
		case "MaaSSubscription":
			doc.sub = subscriptionFromUnstructured(item)
		case "MaaSAuthPolicy":
			doc.policy = authPolicyFromUnstructured(item)
		}
	}
	if doc.sub == nil || doc.sub.GetId() != id {
		return nil, fmt.Errorf("no MaaSSubscription %q in the file", id)
	}

	meta := doc.sub.GetMetadata()
	version, err := strconv.ParseInt(meta.GetAnnotations()[gitVersionAnnotation], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("annotation %s: %w", gitVersionAnnotation, err)
	}
	meta.Version = int32(version)
	delete(meta.Annotations, gitVersionAnnotation)
	if len(meta.Annotations) == 0 {
		meta.Annotations = nil
	}

	prefix := doc.sub.GetSpec().GetOrganization().GetId() + "/"
	if owner := doc.sub.GetSpec().GetOwner(); owner != nil {
		for i, group := range owner.Groups {
			owner.Groups[i] = strings.TrimPrefix(group, prefix)
		}
	}
	return doc, nil
}

func gitError(op, id string, err error) error {
	switch {
	case errors.Is(err, gitops.ErrNotFound):
		return connect.Errorf(connect.CodeNotFound, "Subscription %q not found", id).WithCause(err)
	case errors.Is(err, gitops.ErrConflict):
		return staleSubscriptionError(op, id).WithCause(err)
	default:
		return connect.Errorf(connect.CodeUnavailable, "%s Subscription %q: git: %v", op, id, err).WithCause(err)
	}
}
