package servers

import (
	"context"
	"testing"

	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
	"google.golang.org/grpc/codes"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestClustersListMapsGridClusterShape(t *testing.T) {
	t.Parallel()

	client := newManagedClusterClient(
		managedClusterWithCondition("site1", map[string]string{
			"ai-grid/enabled":     "true",
			"ai-grid/provisioned": "true",
			"location":            "site1",
		}, true, "True"),
		managedClusterWithCondition("site4", map[string]string{
			"location": "site4",
		}, true, "True"),
	)
	mcServer := mustManagedClustersServer(t, client)
	server, err := NewClustersServer().SetManagedClusters(mcServer).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	resp, err := server.List(context.Background(), &aigridv1.ClustersListRequest{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if resp.GetTotal() != 2 || resp.GetSize() != 2 || len(resp.GetItems()) != 2 {
		t.Fatalf("list size/total/items = %d/%d/%d", resp.GetSize(), resp.GetTotal(), len(resp.GetItems()))
	}

	byName := map[string]*aigridv1.GridCluster{}
	for _, item := range resp.GetItems() {
		byName[item.GetName()] = item
	}
	site1 := byName["site1"]
	if site1 == nil {
		t.Fatal("missing site1")
	}
	if site1.GetStatus() != "Ready" || !site1.GetAiGridEnabled() || !site1.GetProvisioned() ||
		site1.GetProvisioningStatus() != "Provisioned" || site1.GetLocation() != "site1" {
		t.Fatalf("site1 = %+v", site1)
	}
	site4 := byName["site4"]
	if site4 == nil {
		t.Fatal("missing site4")
	}
	if site4.GetAiGridEnabled() || site4.GetProvisioned() || site4.GetProvisioningStatus() != "Not provisioned" {
		t.Fatalf("site4 = %+v", site4)
	}
}

func TestClustersUpdateEnroll(t *testing.T) {
	t.Parallel()

	client := newManagedClusterClient(
		managedClusterWithCondition("cluster-b", map[string]string{
			"location": "site2",
			"team":     "platform",
		}, true, "False"),
	)
	mcServer := mustManagedClustersServer(t, client)
	server, err := NewClustersServer().SetManagedClusters(mcServer).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	enabled := true
	resp, err := server.Update(context.Background(), &aigridv1.ClustersUpdateRequest{
		Name:          "cluster-b",
		AiGridEnabled: &enabled,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	got := resp.GetObject()
	if got.GetName() != "cluster-b" || got.GetStatus() != "Offline" || !got.GetAiGridEnabled() ||
		got.GetProvisioned() || got.GetProvisioningStatus() != "Provisioning" || got.GetLocation() != "site2" {
		t.Fatalf("enrolled = %+v", got)
	}

	// Labels merge must preserve existing hub labels.
	mc, err := mcServer.Get(context.Background(), &aigridv1.ManagedClustersGetRequest{Id: "cluster-b"})
	if err != nil {
		t.Fatalf("Get after enroll: %v", err)
	}
	labels := mc.GetObject().GetMetadata().GetLabels()
	if labels["ai-grid/enabled"] != "true" || labels["location"] != "site2" || labels["team"] != "platform" {
		t.Fatalf("labels after enroll = %v", labels)
	}
}

func TestClustersUpdateRejectsNonEnrollBodies(t *testing.T) {
	t.Parallel()

	client := newManagedClusterClient(managedCluster("cluster-b", nil, true))
	mcServer := mustManagedClustersServer(t, client)
	server, err := NewClustersServer().SetManagedClusters(mcServer).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	t.Run("missing ai_grid_enabled", func(t *testing.T) {
		t.Parallel()
		_, err := server.Update(context.Background(), &aigridv1.ClustersUpdateRequest{Name: "cluster-b"})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})
	t.Run("ai_grid_enabled false", func(t *testing.T) {
		t.Parallel()
		disabled := false
		_, err := server.Update(context.Background(), &aigridv1.ClustersUpdateRequest{
			Name:          "cluster-b",
			AiGridEnabled: &disabled,
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})
	t.Run("missing cluster", func(t *testing.T) {
		t.Parallel()
		enabled := true
		_, err := server.Update(context.Background(), &aigridv1.ClustersUpdateRequest{
			Name:          "missing",
			AiGridEnabled: &enabled,
		})
		assertGRPCCode(t, err, codes.NotFound)
	})
}

func managedClusterWithCondition(name string, labels map[string]string, accepts bool, availableStatus string) runtime.Object {
	obj := managedCluster(name, labels, accepts)
	_ = unstructured.SetNestedSlice(obj.Object, []interface{}{
		map[string]interface{}{
			"type":   "ManagedClusterConditionAvailable",
			"status": availableStatus,
		},
	}, "status", "conditions")
	return obj
}
