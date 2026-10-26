// Package openshift_test checks the RBAC that deploy/openshift grants the
// backend's ServiceAccount.
package openshift_test

import (
	"errors"
	"io"
	"os"
	"slices"
	"testing"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

// clusterWriteAllowed lists the cluster-scoped writes the Clusters API needs:
// ManagedCluster CRUD, joining the default ManagedClusterSet on create, and
// accepting the cluster. Every other write must be a namespaced Role.
var clusterWriteAllowed = map[string]bool{
	"cluster.open-cluster-management.io/managedclusters":         true,
	"cluster.open-cluster-management.io/managedclustersets/join": true,
	"register.open-cluster-management.io/managedclusters/accept": true,
}

var writeVerbs = []string{"create", "update", "patch", "delete", "deletecollection", "*"}

type rule struct {
	APIGroups []string `json:"apiGroups"`
	Resources []string `json:"resources"`
	Verbs     []string `json:"verbs"`
}

type object struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Rules     []rule   `json:"rules"`
	Resources []string `json:"resources"`
}

func readObjects(t *testing.T, path string) []object {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	dec := utilyaml.NewYAMLOrJSONDecoder(f, 4096)
	var out []object
	for {
		var obj object
		if err := dec.Decode(&obj); err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}
			t.Fatalf("decode %s: %v", path, err)
		}
		if obj.Kind != "" {
			out = append(out, obj)
		}
	}
}

// TestRBACNoClusterScopedWrites fails on any ClusterRole rule in the
// kustomization's resources that grants a write verb, other than the
// ManagedCluster rules the Clusters API needs. Model and subscription writes
// belong in Roles in their own namespaces.
func TestRBACNoClusterScopedWrites(t *testing.T) {
	kustomizations := readObjects(t, "kustomization.yaml")
	if len(kustomizations) != 1 {
		t.Fatalf("kustomization.yaml: got %d documents, want 1", len(kustomizations))
	}
	clusterRoles := 0
	for _, path := range kustomizations[0].Resources {
		for _, obj := range readObjects(t, path) {
			if obj.Kind != "ClusterRole" {
				continue
			}
			clusterRoles++
			for _, r := range obj.Rules {
				write := slices.ContainsFunc(r.Verbs, func(v string) bool { return slices.Contains(writeVerbs, v) })
				if !write {
					continue
				}
				for _, g := range r.APIGroups {
					for _, res := range r.Resources {
						if !clusterWriteAllowed[g+"/"+res] {
							t.Errorf("%s: ClusterRole %s grants %v on %s/%s in every namespace; use a namespaced Role",
								path, obj.Metadata.Name, r.Verbs, g, res)
						}
					}
				}
			}
		}
	}
	if clusterRoles == 0 {
		t.Fatal("found no ClusterRole in the kustomization's resources")
	}
}
