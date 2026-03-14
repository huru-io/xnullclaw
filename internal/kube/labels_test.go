package kube

import "testing"

func TestAgentLabels(t *testing.T) {
	labels := agentLabels("abc123", "Alice")
	if labels["app.kubernetes.io/managed-by"] != "xnc" {
		t.Errorf("managed-by = %q", labels["app.kubernetes.io/managed-by"])
	}
	if labels["xnc.io/component"] != "agent" {
		t.Errorf("component = %q", labels["xnc.io/component"])
	}
	// Name should be canonicalized (lowercased).
	if labels["xnc.io/agent"] != "alice" {
		t.Errorf("agent = %q, want %q", labels["xnc.io/agent"], "alice")
	}
	if labels["xnc.io/instance"] != "abc123" {
		t.Errorf("instance = %q", labels["xnc.io/instance"])
	}
}

func TestInstanceLabelsFor(t *testing.T) {
	labels := instanceLabelsFor("abc123")
	if labels["app.kubernetes.io/managed-by"] != "xnc" {
		t.Errorf("managed-by = %q", labels["app.kubernetes.io/managed-by"])
	}
	if labels["xnc.io/component"] != "agent" {
		t.Errorf("component = %q", labels["xnc.io/component"])
	}
	if labels["xnc.io/instance"] != "abc123" {
		t.Errorf("instance = %q", labels["xnc.io/instance"])
	}
	// Should NOT have agent label.
	if _, ok := labels["xnc.io/agent"]; ok {
		t.Error("instanceLabelsFor should not include agent label")
	}
}

func TestLabelsConsistency(t *testing.T) {
	// Verify that agentLabels is a superset of instanceLabelsFor.
	agentL := agentLabels("xyz", "bob")
	instL := instanceLabelsFor("xyz")

	for k, v := range instL {
		if agentL[k] != v {
			t.Errorf("agentLabels[%q] = %q, instanceLabels[%q] = %q", k, agentL[k], k, v)
		}
	}
}
