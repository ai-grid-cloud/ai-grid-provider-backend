package servers

import (
	"testing"

	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
)

func TestGridClusterFromManagedCluster(t *testing.T) {
	t.Parallel()

	loc := "site1"
	tests := []struct {
		name             string
		mc               *aigridv1.ManagedCluster
		wantStatus       string
		wantEnabled      bool
		wantProvisioned  bool
		wantProvisioning string
		wantLocation     *string
		wantName         string
	}{
		{
			name: "ready member provisioned",
			mc: &aigridv1.ManagedCluster{
				Id: "site1",
				Metadata: &aigridv1.Metadata{
					Name: "site1",
					Labels: map[string]string{
						"ai-grid/enabled":     "true",
						"ai-grid/provisioned": "true",
						"location":            "site1",
					},
				},
				Status: &aigridv1.ManagedClusterStatus{Available: true, AvailableCondition: "True"},
			},
			wantName:         "site1",
			wantStatus:       "Ready",
			wantEnabled:      true,
			wantProvisioned:  true,
			wantProvisioning: "Provisioned",
			wantLocation:     &loc,
		},
		{
			name: "enabled but not provisioned → Provisioning",
			mc: &aigridv1.ManagedCluster{
				Id: "cluster-b",
				Metadata: &aigridv1.Metadata{
					Labels: map[string]string{
						"ai-grid/enabled": "true",
						"location":        "site2",
					},
				},
				Status: &aigridv1.ManagedClusterStatus{AvailableCondition: "False"},
			},
			wantName:         "cluster-b",
			wantStatus:       "Offline",
			wantEnabled:      true,
			wantProvisioned:  false,
			wantProvisioning: "Provisioning",
			wantLocation:     strPtr("site2"),
		},
		{
			name: "not enrolled",
			mc: &aigridv1.ManagedCluster{
				Id: "site4",
				Metadata: &aigridv1.Metadata{
					Labels: map[string]string{"ai-grid/enabled": "false", "ai-grid/provisioned": "false"},
				},
				Status: &aigridv1.ManagedClusterStatus{AvailableCondition: "True"},
			},
			wantName:         "site4",
			wantStatus:       "Ready",
			wantEnabled:      false,
			wantProvisioned:  false,
			wantProvisioning: "Not provisioned",
		},
		{
			name: "enabled label must be exact lowercase true",
			mc: &aigridv1.ManagedCluster{
				Id:       "c",
				Metadata: &aigridv1.Metadata{Labels: map[string]string{"ai-grid/enabled": "True"}},
				Status:   &aigridv1.ManagedClusterStatus{AvailableCondition: "True"},
			},
			wantName:         "c",
			wantStatus:       "Ready",
			wantEnabled:      false,
			wantProvisioning: "Not provisioned",
		},
		{
			name:             "unknown when condition missing",
			mc:               &aigridv1.ManagedCluster{Id: "x", Status: &aigridv1.ManagedClusterStatus{}},
			wantName:         "x",
			wantStatus:       "Unknown",
			wantProvisioning: "Not provisioned",
		},
		{
			name:             "nil managed cluster",
			mc:               nil,
			wantStatus:       "Unknown",
			wantProvisioning: "Not provisioned",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := gridClusterFromManagedCluster(tc.mc)
			if got.GetName() != tc.wantName {
				t.Fatalf("name = %q, want %q", got.GetName(), tc.wantName)
			}
			if got.GetStatus() != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got.GetStatus(), tc.wantStatus)
			}
			if got.GetAiGridEnabled() != tc.wantEnabled {
				t.Fatalf("ai_grid_enabled = %v, want %v", got.GetAiGridEnabled(), tc.wantEnabled)
			}
			if got.GetProvisioned() != tc.wantProvisioned {
				t.Fatalf("provisioned = %v, want %v", got.GetProvisioned(), tc.wantProvisioned)
			}
			if got.GetProvisioningStatus() != tc.wantProvisioning {
				t.Fatalf("provisioning_status = %q, want %q", got.GetProvisioningStatus(), tc.wantProvisioning)
			}
			if tc.wantLocation == nil {
				if got.Location != nil {
					t.Fatalf("location = %v, want unset", got.GetLocation())
				}
			} else if got.GetLocation() != *tc.wantLocation {
				t.Fatalf("location = %q, want %q", got.GetLocation(), *tc.wantLocation)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
