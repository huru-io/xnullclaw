package kube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jotavich/xnullclaw/internal/docker"
)

func newTestOps(t *testing.T, handler http.Handler) *KubeOps {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client := NewFromConfig(srv.URL, "test-token", "default", srv.Client())
	return NewOps(client, "abc123", "test-image:latest")
}

func TestKubeOps_StartContainer(t *testing.T) {
	var createdResources []string

	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			createdResources = append(createdResources, r.URL.Path)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("{}"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	err := ops.StartContainer(context.Background(), "xnc-abc123-alice", docker.ContainerOpts{
		Image:      "test-image:latest",
		Cmd:        []string{"gateway"},
		AgentName:  "alice",
		ExposePort: true,
		Env:        []string{"KEY=val"},
	})
	if err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	// Should create PVC, Pod, and Service.
	if len(createdResources) != 3 {
		t.Fatalf("expected 3 creates, got %d: %v", len(createdResources), createdResources)
	}
}

func TestKubeOps_StopContainer(t *testing.T) {
	deleted := false
	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = true
			w.WriteHeader(http.StatusOK)
			return
		}
	}))

	if err := ops.StopContainer(context.Background(), "xnc-abc123-alice"); err != nil {
		t.Fatalf("StopContainer: %v", err)
	}
	if !deleted {
		t.Error("expected pod to be deleted")
	}
}

func TestKubeOps_IsRunning_True(t *testing.T) {
	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pod := Pod{Status: PodStatus{Phase: "Running"}}
		json.NewEncoder(w).Encode(pod)
	}))

	running, err := ops.IsRunning(context.Background(), "xnc-abc123-alice")
	if err != nil {
		t.Fatalf("IsRunning: %v", err)
	}
	if !running {
		t.Error("expected pod to be running")
	}
}

func TestKubeOps_IsRunning_NotFound(t *testing.T) {
	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"code": 404, "reason": "NotFound", "message": "not found"})
	}))

	running, err := ops.IsRunning(context.Background(), "xnc-abc123-alice")
	if err != nil {
		t.Fatalf("IsRunning: %v", err)
	}
	if running {
		t.Error("expected pod to not be running")
	}
}

func TestKubeOps_RemoveContainer(t *testing.T) {
	var deletedResources []string
	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletedResources = append(deletedResources, r.URL.Path)
			w.WriteHeader(http.StatusOK)
		}
	}))

	if err := ops.RemoveContainer(context.Background(), "xnc-abc123-alice", true); err != nil {
		t.Fatalf("RemoveContainer: %v", err)
	}

	// Should delete pods, services, pvcs.
	if len(deletedResources) != 3 {
		t.Fatalf("expected 3 deletes, got %d: %v", len(deletedResources), deletedResources)
	}
}

func TestKubeOps_MappedPort(t *testing.T) {
	ops := NewOps(nil, "abc123", "test:latest")
	port, err := ops.MappedPort(context.Background(), "xnc-abc123-alice")
	if err != nil {
		t.Fatalf("MappedPort: %v", err)
	}
	if port != gatewayPort {
		t.Errorf("port = %d, want %d", port, gatewayPort)
	}
}

func TestKubeOps_ListContainers(t *testing.T) {
	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list := PodList{Items: []Pod{
			{Metadata: ObjectMeta{Name: "xnc-abc123-alice"}, Status: PodStatus{Phase: "Running"}},
			{Metadata: ObjectMeta{Name: "xnc-abc123-bob"}, Status: PodStatus{Phase: "Pending"}},
		}}
		json.NewEncoder(w).Encode(list)
	}))

	containers, err := ops.ListContainers(context.Background(), "xnc-abc123-")
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}
	if containers[0].Name != "xnc-abc123-alice" {
		t.Errorf("first = %q, want %q", containers[0].Name, "xnc-abc123-alice")
	}
}

func TestKubeOps_UnsupportedOps(t *testing.T) {
	ops := NewOps(nil, "abc123", "test:latest")

	if err := ops.CopyToContainer(context.Background(), "", "", nil); err != docker.ErrNotSupported {
		t.Errorf("CopyToContainer: got %v, want ErrNotSupported", err)
	}
	if _, err := ops.CopyFromContainer(context.Background(), "", ""); err != docker.ErrNotSupported {
		t.Errorf("CopyFromContainer: got %v, want ErrNotSupported", err)
	}
	if err := ops.AttachInteractive(context.Background(), "", nil); err != docker.ErrNotSupported {
		t.Errorf("AttachInteractive: got %v, want ErrNotSupported", err)
	}
	if err := ops.ImageTag(context.Background(), "", ""); err != docker.ErrNotSupported {
		t.Errorf("ImageTag: got %v, want ErrNotSupported", err)
	}
	if err := ops.ImageBuild(context.Background(), "", docker.BuildOpts{}); err != docker.ErrNotSupported {
		t.Errorf("ImageBuild: got %v, want ErrNotSupported", err)
	}
}

func TestKubeOps_StopContainer_NotFound(t *testing.T) {
	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"code": 404, "reason": "NotFound", "message": "not found"})
	}))

	// StopContainer should tolerate 404 (pod already gone).
	if err := ops.StopContainer(context.Background(), "xnc-abc123-gone"); err != nil {
		t.Errorf("StopContainer with 404 should return nil, got: %v", err)
	}
}

func TestKubeOps_ListContainers_PrefixFilter(t *testing.T) {
	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list := PodList{Items: []Pod{
			{Metadata: ObjectMeta{Name: "xnc-abc123-alice"}, Status: PodStatus{Phase: "Running"}},
			{Metadata: ObjectMeta{Name: "xnc-xyz789-carol"}, Status: PodStatus{Phase: "Running"}},
		}}
		json.NewEncoder(w).Encode(list)
	}))

	containers, err := ops.ListContainers(context.Background(), "xnc-abc123-")
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("expected 1 container after prefix filter, got %d", len(containers))
	}
	if containers[0].Name != "xnc-abc123-alice" {
		t.Errorf("filtered container = %q", containers[0].Name)
	}
}

func TestKubeOps_StartContainer_RollbackOnPodFailure(t *testing.T) {
	var createdResources, deletedResources []string
	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Allow PVC creation, fail on Pod creation.
			if strings.Contains(r.URL.Path, "/pods") {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{"code": 403, "reason": "Forbidden", "message": "forbidden"})
				return
			}
			createdResources = append(createdResources, r.URL.Path)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("{}"))
			return
		}
		if r.Method == http.MethodDelete {
			deletedResources = append(deletedResources, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			return
		}
	}))

	err := ops.StartContainer(context.Background(), "xnc-abc123-alice", docker.ContainerOpts{
		Image:     "test:latest",
		AgentName: "alice",
	})
	if err == nil {
		t.Fatal("expected error when pod creation fails")
	}

	// PVC should be rolled back.
	if len(deletedResources) != 1 {
		t.Fatalf("expected 1 rollback delete (PVC), got %d: %v", len(deletedResources), deletedResources)
	}
	if !strings.Contains(deletedResources[0], "persistentvolumeclaims") {
		t.Errorf("expected PVC deletion, got %q", deletedResources[0])
	}
}

func TestKubeOps_StartContainer_RollbackOnServiceFailure(t *testing.T) {
	var deletedResources []string
	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Allow PVC and Pod creation, fail on Service creation.
			if strings.Contains(r.URL.Path, "/services") {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{"code": 403, "reason": "Forbidden", "message": "forbidden"})
				return
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("{}"))
			return
		}
		if r.Method == http.MethodDelete {
			deletedResources = append(deletedResources, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			return
		}
	}))

	err := ops.StartContainer(context.Background(), "xnc-abc123-alice", docker.ContainerOpts{
		Image:      "test:latest",
		AgentName:  "alice",
		ExposePort: true,
	})
	if err == nil {
		t.Fatal("expected error when service creation fails")
	}

	// Both Pod and PVC should be rolled back.
	if len(deletedResources) != 2 {
		t.Fatalf("expected 2 rollback deletes (Pod + PVC), got %d: %v", len(deletedResources), deletedResources)
	}
	hasPod, hasPVC := false, false
	for _, d := range deletedResources {
		if strings.Contains(d, "/pods/") {
			hasPod = true
		}
		if strings.Contains(d, "/persistentvolumeclaims/") {
			hasPVC = true
		}
	}
	if !hasPod {
		t.Error("expected Pod rollback deletion")
	}
	if !hasPVC {
		t.Error("expected PVC rollback deletion")
	}
}

func TestKubeOps_NoopOps(t *testing.T) {
	ops := NewOps(nil, "abc123", "test:latest")

	if err := ops.EnsureNetwork(context.Background(), "net"); err != nil {
		t.Errorf("EnsureNetwork: %v", err)
	}
	if err := ops.ConnectNetwork(context.Background(), "net", "ctr"); err != nil {
		t.Errorf("ConnectNetwork: %v", err)
	}
	if err := ops.ImagePull(context.Background(), "img"); err != nil {
		t.Errorf("ImagePull: %v", err)
	}
	if err := ops.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if exists, err := ops.ImageExists(context.Background(), "img"); err != nil || !exists {
		t.Errorf("ImageExists: exists=%v, err=%v", exists, err)
	}
}

func TestKubeOps_InspectContainer_WaitingState(t *testing.T) {
	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pod := Pod{
			Metadata: ObjectMeta{Name: "xnc-abc123-alice"},
			Status: PodStatus{
				Phase: "Pending",
				ContainerStatuses: []ContainerStatus{{
					Name:  "agent",
					Ready: false,
					State: ContainerState{
						Waiting: &ContainerStateWaiting{Reason: "ImagePullBackOff"},
					},
				}},
			},
		}
		json.NewEncoder(w).Encode(pod)
	}))

	info, err := ops.InspectContainer(context.Background(), "xnc-abc123-alice")
	if err != nil {
		t.Fatalf("InspectContainer: %v", err)
	}
	if info.State != "pending" {
		t.Errorf("state = %q, want %q", info.State, "pending")
	}
	// Status should be the Waiting reason, not the phase.
	if info.Status != "ImagePullBackOff" {
		t.Errorf("status = %q, want %q", info.Status, "ImagePullBackOff")
	}
}

func TestKubeOps_InspectContainer_NotFound(t *testing.T) {
	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"code": 404, "reason": "NotFound", "message": "not found"})
	}))

	_, err := ops.InspectContainer(context.Background(), "xnc-abc123-gone")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found: %v", err)
	}
}

func TestKubeOps_StartContainer_NoExposePort(t *testing.T) {
	var createdResources []string

	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			createdResources = append(createdResources, r.URL.Path)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("{}"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	err := ops.StartContainer(context.Background(), "xnc-abc123-alice", docker.ContainerOpts{
		Image:      "test-image:latest",
		Cmd:        []string{"gateway"},
		AgentName:  "alice",
		ExposePort: false, // no service should be created
		Env:        []string{"KEY=val"},
	})
	if err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	// Should create PVC and Pod only — no Service.
	if len(createdResources) != 2 {
		t.Fatalf("expected 2 creates (PVC + Pod), got %d: %v", len(createdResources), createdResources)
	}
	for _, r := range createdResources {
		if strings.Contains(r, "/services") {
			t.Error("Service should NOT be created when ExposePort=false")
		}
	}
}

func TestKubeOps_InspectContainer(t *testing.T) {
	ops := newTestOps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pod := Pod{
			Metadata: ObjectMeta{Name: "xnc-abc123-alice"},
			Status: PodStatus{
				Phase: "Running",
				ContainerStatuses: []ContainerStatus{{
					Name:  "agent",
					Ready: true,
					State: ContainerState{
						Running: &ContainerStateRunning{StartedAt: "2026-01-01T00:00:00Z"},
					},
				}},
			},
		}
		json.NewEncoder(w).Encode(pod)
	}))

	info, err := ops.InspectContainer(context.Background(), "xnc-abc123-alice")
	if err != nil {
		t.Fatalf("InspectContainer: %v", err)
	}
	if info.Name != "xnc-abc123-alice" {
		t.Errorf("name = %q", info.Name)
	}
	if info.State != "running" {
		t.Errorf("state = %q, want %q", info.State, "running")
	}
}
