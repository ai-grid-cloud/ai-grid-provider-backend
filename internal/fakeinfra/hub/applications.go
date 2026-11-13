package hub

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Argo CD Applications the fake hub serves as is from applications.json, by
// namespace: the catalog's Applications whose status.resources[] the backend
// reads models' and subscriptions' status from (README, "Status from the
// spokes"). Static: the fixtures say what each cluster reports.
const (
	applicationsPath      = "/apis/argoproj.io/v1alpha1/namespaces"
	applicationAPIVersion = "argoproj.io/v1alpha1"
	applicationKind       = "Application"
	applicationListKind   = "ApplicationList"
)

var applicationGroupResource = schema.GroupResource{Group: "argoproj.io", Resource: "applications"}

func (h *Hub) listApplications(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, "list", "") {
		return
	}
	namespace := r.PathValue("namespace")
	h.mu.Lock()
	names := make([]string, 0, len(h.applications[namespace]))
	for name := range h.applications[namespace] {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]any, 0, len(names)) // *unstructured.Unstructured, then their Object
	for _, name := range names {
		items = append(items, h.applications[namespace][name])
	}
	h.mu.Unlock()
	expand := h.generatedResources(r.Context())
	for i, app := range items {
		items[i] = expand(app.(*unstructured.Unstructured)).Object
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion": applicationAPIVersion,
		"kind":       applicationListKind,
		"metadata":   map[string]any{"resourceVersion": "1"},
		"items":      items,
	})
}

func (h *Hub) getApplication(w http.ResponseWriter, r *http.Request) {
	namespace, name := r.PathValue("namespace"), r.PathValue("name")
	if !h.authorize(w, r, "get", name) {
		return
	}
	h.mu.Lock()
	app, ok := h.applications[namespace][name]
	h.mu.Unlock()
	if !ok {
		writeStatus(w, apierrors.NewNotFound(applicationGroupResource, name))
		return
	}
	writeJSON(w, http.StatusOK, h.generatedResources(r.Context())(app).Object)
}

// generatedSuffix is the backend's generated subscription suffix: "-" and six
// lower-case letters or digits (internal/servers, assignGeneratedSubscriptionID).
const generatedSuffix = `-[a-z0-9]{6}`

// generatedResources returns a function that copies an Application and adds,
// for each MaaSSubscription its status.resources report as base (and each
// MaaSAuthPolicy as base-policy), the same entry for every catalog
// subscription named base-<suffix> (and base-<suffix>-policy). The backend
// gives every created subscription such a name, which static fixtures can't
// know. Without WithSubscriptionIDs, or when the catalog can't be read (logged),
// the copy is the fixture's.
func (h *Hub) generatedResources(ctx context.Context) func(*unstructured.Unstructured) *unstructured.Unstructured {
	var ids []string
	if h.subscriptionIDs != nil {
		var err error
		if ids, err = h.subscriptionIDs(ctx); err != nil {
			h.logger.Warn("list catalog subscriptions for Application status", slog.Any("error", err))
			ids = nil
		}
	}
	return func(app *unstructured.Unstructured) *unstructured.Unstructured {
		out := app.DeepCopy()
		if len(ids) == 0 {
			return out
		}
		resources, found, _ := unstructured.NestedSlice(out.Object, "status", "resources")
		if !found {
			return out
		}
		expanded := make([]any, 0, len(resources))
		for _, raw := range resources {
			expanded = append(expanded, raw)
			res, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			kind, _ := res["kind"].(string)
			name, _ := res["name"].(string)
			var base, tail string
			switch kind {
			case maasSubscriptionKind:
				base = name
			case maasAuthPolicyKind:
				var ok bool
				if base, ok = strings.CutSuffix(name, "-policy"); !ok {
					continue
				}
				tail = "-policy"
			default:
				continue
			}
			pattern := regexp.MustCompile("^" + regexp.QuoteMeta(base) + generatedSuffix + "$")
			for _, id := range ids {
				if pattern.MatchString(id) {
					copied := runtimeDeepCopy(res)
					copied["name"] = id + tail
					expanded = append(expanded, copied)
				}
			}
		}
		_ = unstructured.SetNestedSlice(out.Object, expanded, "status", "resources")
		return out
	}
}

func runtimeDeepCopy(in map[string]any) map[string]any {
	return (&unstructured.Unstructured{Object: in}).DeepCopy().Object
}

// seedApplicationFixtures stores the fixture Applications by namespace and
// name. Each needs a name and a namespace; a missing resourceVersion becomes
// "1".
func seedApplicationFixtures(h *Hub, fx *Fixtures) error {
	for i := range fx.Applications {
		app := fx.Applications[i].DeepCopy()
		name, namespace := app.GetName(), app.GetNamespace()
		if name == "" || namespace == "" {
			return fmt.Errorf("fixture Application %d: metadata.name and metadata.namespace are required", i)
		}
		if h.applications[namespace] == nil {
			h.applications[namespace] = map[string]*unstructured.Unstructured{}
		}
		if _, dup := h.applications[namespace][name]; dup {
			return fmt.Errorf("fixture Application %s/%s listed twice", namespace, name)
		}
		if app.GetResourceVersion() == "" {
			app.SetResourceVersion("1")
		}
		h.applications[namespace][name] = app
	}
	return nil
}
