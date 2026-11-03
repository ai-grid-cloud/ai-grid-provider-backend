package hub

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

const configMapsPath = "/api/v1/namespaces/{namespace}/configmaps/{name}"

var configMapGroupResource = schema.GroupResource{Resource: "configmaps"}

// configMapNameRE is a Kubernetes object name (DNS-1123 subdomain).
var configMapNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// WithConfigMapDir serves ConfigMaps from dir: GET of ConfigMap NAME in
// NAMESPACE reads dir/NAMESPACE/NAME.yaml, a ConfigMap manifest, on every
// request, so an edited or added file applies without a restart. A missing
// file is NotFound. Without a dir every ConfigMap is NotFound.
func WithConfigMapDir(dir string) Option { return func(h *Hub) { h.configMapDir = dir } }

func (h *Hub) getConfigMap(w http.ResponseWriter, r *http.Request) {
	namespace, name := r.PathValue("namespace"), r.PathValue("name")
	user, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !h.allowed(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
		writeStatus(w, apierrors.NewForbidden(configMapGroupResource, name,
			verbatim(fmt.Sprintf("User %q cannot get resource \"configmaps\" in API group \"\" in the namespace %q", user, namespace))))
		return
	}
	// ServeMux unescapes a path value, so %2F reaches here as "/": take only
	// Kubernetes object names, which hold no "/" and can't be "." or "..".
	if h.configMapDir == "" || !configMapNameRE.MatchString(namespace) || !configMapNameRE.MatchString(name) {
		writeStatus(w, apierrors.NewNotFound(configMapGroupResource, name))
		return
	}
	raw, err := os.ReadFile(filepath.Join(h.configMapDir, namespace, name+".yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		writeStatus(w, apierrors.NewNotFound(configMapGroupResource, name))
		return
	}
	var object map[string]any
	if err == nil {
		err = yaml.Unmarshal(raw, &object)
	}
	if err != nil {
		writeStatus(w, apierrors.NewInternalError(err))
		return
	}
	writeJSON(w, http.StatusOK, object)
}
