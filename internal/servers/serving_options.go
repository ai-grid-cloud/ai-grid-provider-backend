package servers

import (
	"errors"
	"fmt"
	"regexp"

	"sigs.k8s.io/yaml"

	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
)

// ServingOptions are the accelerator configurations a model can use, the
// values its spec.base_refs takes: one list for every cluster. The backend
// never reads the clusters for it; it reads two ConfigMaps on the hub
// (ConfigMapServingOptions): the defaults the console's Helm chart ships and a
// custom one the provider admin owns.
type ServingOptions struct {
	BaseRefs []BaseRefOption `json:"baseRefs"`
}

// BaseRefOption is an OpenShift AI accelerator preset by its full name.
type BaseRefOption struct {
	Name         string   `json:"name"`
	DisplayName  string   `json:"displayName,omitempty"`
	Description  string   `json:"description,omitempty"`
	Accelerators []string `json:"accelerators,omitempty"`
}

// dnsSubdomainRE is a Kubernetes object name (DNS-1123 subdomain).
var dnsSubdomainRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// parseServingOptions reads a list in YAML or JSON. Unknown fields, a missing
// or invalid name, and a name listed twice are errors, so a mistyped value is
// reported instead of hiding a choice.
func parseServingOptions(data []byte) (*ServingOptions, error) {
	var opts ServingOptions
	if err := yaml.UnmarshalStrict(data, &opts); err != nil {
		return nil, err
	}
	var errs []error
	seen := map[string]bool{}
	for i, s := range opts.BaseRefs {
		if !dnsSubdomainRE.MatchString(s.Name) || len(s.Name) > 253 {
			errs = append(errs, fmt.Errorf("baseRefs[%d].name %q is not a Kubernetes object name", i, s.Name))
		}
		if seen[s.Name] {
			errs = append(errs, fmt.Errorf("baseRefs[%d].name %q is listed twice", i, s.Name))
		}
		seen[s.Name] = true
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &opts, nil
}

// merge applies a custom list to the defaults by name: a custom entry
// replaces the default of the same name in place, and any other custom entry
// is added after the defaults.
func (o *ServingOptions) merge(custom *ServingOptions) *ServingOptions {
	byName := map[string]BaseRefOption{}
	for _, c := range custom.BaseRefs {
		byName[c.Name] = c
	}
	used := map[string]bool{}
	out := make([]BaseRefOption, 0, len(o.BaseRefs)+len(custom.BaseRefs))
	for _, d := range o.BaseRefs {
		if c, ok := byName[d.Name]; ok {
			used[d.Name] = true
			d = c
		}
		out = append(out, d)
	}
	for _, c := range custom.BaseRefs {
		if !used[c.Name] {
			out = append(out, c)
		}
	}
	return &ServingOptions{BaseRefs: out}
}

func (o *ServingOptions) baseRef(name string) bool {
	if o == nil {
		return false
	}
	for _, s := range o.BaseRefs {
		if s.Name == name {
			return true
		}
	}
	return false
}

func (o *ServingOptions) toProto() *aigridv1.ModelsListServingOptionsResponse {
	out := &aigridv1.ModelsListServingOptionsResponse{}
	if o == nil {
		return out
	}
	for _, s := range o.BaseRefs {
		out.BaseRefs = append(out.BaseRefs, &aigridv1.BaseRefOption{
			Name: s.Name, DisplayName: s.DisplayName, Description: s.Description, Accelerators: s.Accelerators,
		})
	}
	return out
}
