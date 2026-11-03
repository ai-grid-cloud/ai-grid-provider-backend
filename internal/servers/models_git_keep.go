package servers

import "k8s.io/apimachinery/pkg/runtime"

// The LLMInferenceService fields the Models API owns. On Update the backend
// replaces or removes these and keeps every other field of the file in Git,
// so a field someone added by hand, such as a volume or a second container,
// survives a console edit.
var (
	modelOwnedSpecKeys      = []string{"replicas", "baseRefs", "runtime", "router", "scaling"}
	modelOwnedModelKeys     = []string{"uri", "name"}
	modelOwnedContainerKeys = []string{"image", "resources", "env"}
)

// keepUnmodeledFields returns rendered, the LLMInferenceService built from the
// API's model, with the fields of previous, the one in Git, that the API
// doesn't own. Containers and volumes match by name (mergeNamedList).
func keepUnmodeledFields(previous, rendered map[string]interface{}) map[string]interface{} {
	out := runtime.DeepCopyJSON(previous)
	out["apiVersion"] = rendered["apiVersion"]
	out["kind"] = rendered["kind"]

	meta := childMap(out, "metadata")
	renderedMeta := childMap(rendered, "metadata")
	for _, key := range []string{"name", "namespace", "labels", "annotations"} {
		setOrDelete(meta, renderedMeta, key)
	}

	spec := childMap(out, "spec")
	renderedSpec := childMap(rendered, "spec")
	for _, key := range modelOwnedSpecKeys {
		setOrDelete(spec, renderedSpec, key)
	}
	model := childMap(spec, "model")
	for _, key := range modelOwnedModelKeys {
		setOrDelete(model, childMap(renderedSpec, "model"), key)
	}

	template := childMap(spec, "template")
	renderedTemplate := childMap(renderedSpec, "template")
	mergeNamedList(template, renderedTemplate, "containers", modelOwnedContainerKeys)
	mergeNamedList(template, renderedTemplate, "volumes", nil)
	if len(template) == 0 {
		delete(spec, "template")
	}
	if len(model) == 0 {
		delete(spec, "model")
	}
	return out
}

// childMap returns m[key] as a map, creating it in m when it is missing or
// isn't a map.
func childMap(m map[string]interface{}, key string) map[string]interface{} {
	child, ok := m[key].(map[string]interface{})
	if !ok {
		child = map[string]interface{}{}
		m[key] = child
	}
	return child
}

func setOrDelete(dst, src map[string]interface{}, key string) {
	if v, ok := src[key]; ok {
		dst[key] = v
	} else {
		delete(dst, key)
	}
}

// mergeNamedList sets dst[key] to the rendered items followed by the previous
// items the API doesn't render, matched by name and kept in their order. A
// rendered item that was already there keeps its previous keys except owned,
// which come from the rendered one; with owned nil, the rendered item replaces
// it whole.
func mergeNamedList(dst, rendered map[string]interface{}, key string, owned []string) {
	previousItems, _ := dst[key].([]interface{})
	renderedItems, _ := rendered[key].([]interface{})
	items := make([]interface{}, 0, len(previousItems)+len(renderedItems))
	renderedNames := map[string]bool{}
	for _, item := range renderedItems {
		r, _ := item.(map[string]interface{})
		name, _ := r["name"].(string)
		renderedNames[name] = true
		merged := r
		if p := itemNamed(previousItems, name); p != nil && owned != nil {
			merged = p
			for _, k := range owned {
				setOrDelete(merged, r, k)
			}
		}
		items = append(items, merged)
	}
	for _, item := range previousItems {
		p, _ := item.(map[string]interface{})
		if name, _ := p["name"].(string); !renderedNames[name] {
			items = append(items, p)
		}
	}
	if len(items) > 0 {
		dst[key] = items
	} else {
		delete(dst, key)
	}
}

func itemNamed(containers []interface{}, name string) map[string]interface{} {
	for _, c := range containers {
		if m, ok := c.(map[string]interface{}); ok && m["name"] == name {
			return m
		}
	}
	return nil
}
