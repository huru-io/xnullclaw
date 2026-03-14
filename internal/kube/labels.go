package kube

import "github.com/jotavich/xnullclaw/internal/agent"

// agentLabels returns the standard K8s labels for all resources belonging to an agent.
// Used by both KubeOps and KubeBackend to ensure consistent labeling.
func agentLabels(instanceID, name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "xnc",
		"xnc.io/component":            "agent",
		"xnc.io/agent":                agent.CanonicalName(name),
		"xnc.io/instance":             instanceID,
	}
}

// instanceLabelsFor returns labels matching all resources for an xnc instance
// (without agent-specific filtering).
func instanceLabelsFor(instanceID string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "xnc",
		"xnc.io/component":            "agent",
		"xnc.io/instance":             instanceID,
	}
}
