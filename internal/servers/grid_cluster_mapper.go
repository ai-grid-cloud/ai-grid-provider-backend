package servers

import aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"

const (
	labelAIGridEnabled     = "ai-grid/enabled"
	labelAIGridProvisioned = "ai-grid/provisioned"
	labelLocation          = "location"

	clusterStatusReady   = "Ready"
	clusterStatusOffline = "Offline"
	clusterStatusUnknown = "Unknown"

	provisioningStatusProvisioned    = "Provisioned"
	provisioningStatusProvisioning   = "Provisioning"
	provisioningStatusNotProvisioned = "Not provisioned"
)

// gridClusterFromManagedCluster maps an OSAC ManagedCluster to the PoC GridCluster shape.
func gridClusterFromManagedCluster(mc *aigridv1.ManagedCluster) *aigridv1.GridCluster {
	if mc == nil {
		return &aigridv1.GridCluster{Status: clusterStatusUnknown, ProvisioningStatus: provisioningStatusNotProvisioned}
	}

	name := managedClusterID(mc)
	labels := map[string]string{}
	if mc.GetMetadata() != nil {
		labels = mc.GetMetadata().GetLabels()
		if name == "" {
			name = mc.GetMetadata().GetName()
		}
	}

	enabled := labels[labelAIGridEnabled] == "true"
	provisioned := labels[labelAIGridProvisioned] == "true"

	out := &aigridv1.GridCluster{
		Name:               name,
		Status:             clusterReadiness(mc.GetStatus()),
		AiGridEnabled:      enabled,
		Provisioned:        provisioned,
		ProvisioningStatus: provisioningStatus(enabled, provisioned),
	}
	if loc := labels[labelLocation]; loc != "" {
		out.Location = &loc
	}
	return out
}

func clusterReadiness(status *aigridv1.ManagedClusterStatus) string {
	if status == nil {
		return clusterStatusUnknown
	}
	// PoC compares Available condition status with exact "True" / "False".
	switch status.GetAvailableCondition() {
	case "True":
		return clusterStatusReady
	case "False":
		return clusterStatusOffline
	default:
		return clusterStatusUnknown
	}
}

func provisioningStatus(aiGridEnabled, provisioned bool) string {
	switch {
	case provisioned:
		return provisioningStatusProvisioned
	case aiGridEnabled:
		return provisioningStatusProvisioning
	default:
		return provisioningStatusNotProvisioned
	}
}

// enrollLabelPatch returns the label merge used for PoC enroll (ai-grid/enabled=true).
func enrollLabelPatch() map[string]string {
	return map[string]string{labelAIGridEnabled: "true"}
}
