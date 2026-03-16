package kube

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jotavich/xnullclaw/internal/docker"
)

// gatewayPort is the port nullclaw listens on inside the container.
const gatewayPort = 3000

// KubeOps implements docker.Ops by managing K8s Pods, Services, etc.
type KubeOps struct {
	client       *Client
	instanceID   string            // 6-hex-char instance ID for resource naming
	image        string            // default container image
	nodeSelector map[string]string // optional nodeSelector for agent pods
	agentRes     agentResources    // resource requests/limits for agent pods
}

// agentResources holds configurable resource values for agent pods.
type agentResources struct {
	cpuRequest string
	cpuLimit   string
	memRequest string
	memLimit   string
	storage    string
}

var _ docker.Ops = (*KubeOps)(nil)

// NewOps creates a KubeOps adapter.
// Reads env vars to configure agent pod scheduling and resources:
//   - XNC_NODE_SELECTOR: "key=value" nodeSelector for agent pods
//   - XNC_AGENT_CPU_REQUEST, XNC_AGENT_CPU_LIMIT: CPU requests/limits (default: 100m/250m)
//   - XNC_AGENT_MEMORY_REQUEST, XNC_AGENT_MEMORY_LIMIT: memory requests/limits (default: 64Mi/128Mi)
//   - XNC_AGENT_STORAGE: PVC size per agent (default: 1Gi)
func NewOps(client *Client, instanceID, image string) *KubeOps {
	ops := &KubeOps{
		client:     client,
		instanceID: instanceID,
		image:      image,
		agentRes: agentResources{
			cpuRequest: envOrDefault("XNC_AGENT_CPU_REQUEST", "50m"),
			cpuLimit:   envOrDefault("XNC_AGENT_CPU_LIMIT", "125m"),
			memRequest: envOrDefault("XNC_AGENT_MEMORY_REQUEST", "32Mi"),
			memLimit:   envOrDefault("XNC_AGENT_MEMORY_LIMIT", "64Mi"),
			storage:    envOrDefault("XNC_AGENT_STORAGE", "512Mi"),
		},
	}
	if sel := os.Getenv("XNC_NODE_SELECTOR"); sel != "" {
		if k, v, ok := strings.Cut(sel, "="); ok && k != "" {
			ops.nodeSelector = map[string]string{k: v}
		}
	}
	return ops
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// labels returns the standard labels for all resources belonging to an agent.
func (k *KubeOps) labels(agentName string) map[string]string {
	return agentLabels(k.instanceID, agentName)
}

// instanceLabels returns labels matching all resources for this xnc instance.
func (k *KubeOps) instanceLabels() map[string]string {
	return instanceLabelsFor(k.instanceID)
}

// ---------------------------------------------------------------------------
// Container lifecycle
// ---------------------------------------------------------------------------

func (k *KubeOps) IsRunning(ctx context.Context, name string) (bool, error) {
	var pod Pod
	if err := k.client.Get(ctx, "pods", name, &pod); err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return pod.Status.Phase == "Running", nil
}

func (k *KubeOps) StartContainer(ctx context.Context, name string, opts docker.ContainerOpts) error {
	agentName := opts.AgentName
	if agentName == "" {
		agentName = name
	}
	labels := k.labels(agentName)

	f := false
	tt := true
	var uid int64 = 1000

	// Create PVC for agent data.
	pvc := PersistentVolumeClaim{
		APIVersion: "v1",
		Kind:       "PersistentVolumeClaim",
		Metadata:   ObjectMeta{Name: name, Labels: labels},
		Spec: PVCSpec{
			AccessModes: []string{"ReadWriteOnce"},
			Resources:   PVCResourceRequirements{Requests: map[string]string{"storage": k.agentRes.storage}},
		},
	}
	if err := k.client.Create(ctx, "persistentvolumeclaims", pvc, nil); err != nil && !IsConflict(err) {
		return fmt.Errorf("create pvc: %w", err)
	}

	// Build env vars from opts.Env (KEY=VALUE strings → EnvVar structs).
	var envVars []EnvVar
	for _, e := range opts.Env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envVars = append(envVars, EnvVar{Name: parts[0], Value: parts[1]})
		}
	}

	// Create Pod.
	pod := Pod{
		APIVersion: "v1",
		Kind:       "Pod",
		Metadata:   ObjectMeta{Name: name, Labels: labels},
		Spec: PodSpec{
			RestartPolicy:                "Always",
			AutomountServiceAccountToken: &f,
			NodeSelector:                 k.nodeSelector,
			SecurityContext: &PodSecurityContext{
				RunAsNonRoot:   &tt,
				RunAsUser:      &uid,
				RunAsGroup:     &uid,
				FSGroup:        &uid,
				SeccompProfile: &SeccompProfile{Type: "RuntimeDefault"},
			},
			Containers: []Container{{
				Name:            "agent",
				Image:           opts.Image,
				ImagePullPolicy: "Always",
				Args:            opts.Cmd,
				Env:     envVars,
				Ports: []ContainerPort{
					{Name: "http", ContainerPort: gatewayPort},
					{Name: "ws", ContainerPort: webChannelPort},
				},
				VolumeMounts: []VolumeMount{
					{Name: "data", MountPath: "/nullclaw-data"},
					{Name: "config", MountPath: "/nullclaw-data/config.json", SubPath: "config.json", ReadOnly: true},
					{Name: "tmp", MountPath: "/tmp"},
				},
				Resources: ResourceRequirements{
					Requests: map[string]string{
						"memory": k.agentRes.memRequest,
						"cpu":    k.agentRes.cpuRequest,
					},
					Limits: map[string]string{
						"memory": k.agentRes.memLimit,
						"cpu":    k.agentRes.cpuLimit,
					},
				},
				SecurityContext: &SecurityContext{
					RunAsNonRoot:             &tt,
					RunAsUser:                &uid,
					RunAsGroup:               &uid,
					ReadOnlyRootFilesystem:   &tt,
					AllowPrivilegeEscalation: &f,
					Capabilities:             &Capabilities{Drop: []string{"ALL"}},
					SeccompProfile:           &SeccompProfile{Type: "RuntimeDefault"},
				},
			}},
			Volumes: []Volume{
				{
					Name:                  "data",
					PersistentVolumeClaim: &PVCVolumeSource{ClaimName: name},
				},
				{
					Name:      "config",
					ConfigMap: &ConfigMapVolumeSource{Name: name},
				},
				{
					Name:     "tmp",
					EmptyDir: &EmptyDirVolumeSource{Medium: "Memory", SizeLimit: "64Mi"},
				},
			},
		},
	}

	if err := k.client.Create(ctx, "pods", pod, nil); err != nil {
		// Rollback PVC on pod creation failure.
		_ = k.client.Delete(ctx, "persistentvolumeclaims", name)
		return fmt.Errorf("create pod: %w", err)
	}

	// Create Service (ClusterIP) for pod-to-pod routing.
	if opts.ExposePort {
		svc := Service{
			APIVersion: "v1",
			Kind:       "Service",
			Metadata:   ObjectMeta{Name: name, Labels: labels},
			Spec: ServiceSpec{
				Type:     "ClusterIP",
				Selector: labels,
				Ports: []ServicePort{
					{Name: "http", Port: gatewayPort, TargetPort: gatewayPort},
					{Name: "ws", Port: webChannelPort, TargetPort: webChannelPort},
				},
			},
		}
		if err := k.client.Create(ctx, "services", svc, nil); err != nil && !IsConflict(err) {
			// Rollback Pod + PVC on service creation failure.
			_ = k.client.Delete(ctx, "pods", name)
			_ = k.client.Delete(ctx, "persistentvolumeclaims", name)
			return fmt.Errorf("create service: %w", err)
		}
	}

	return nil
}

func (k *KubeOps) StopContainer(ctx context.Context, name string) error {
	// Delete pod only — preserve PVC, ConfigMap, Secret, Service.
	err := k.client.Delete(ctx, "pods", name)
	if err != nil && !IsNotFound(err) {
		return err
	}

	// Wait for pod to be fully gone (avoids 409 AlreadyExists on recreate).
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var pod Pod
		if err := k.client.Get(ctx, "pods", name, &pod); err != nil {
			if IsNotFound(err) {
				return nil // pod fully deleted
			}
			return err
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timeout waiting for pod %s to terminate", name)
}

func (k *KubeOps) RemoveContainer(ctx context.Context, name string, force bool) error {
	// Delete all resources for this agent.
	for _, resource := range []string{"pods", "services", "persistentvolumeclaims"} {
		if err := k.client.Delete(ctx, resource, name); err != nil && !IsNotFound(err) {
			return fmt.Errorf("delete %s/%s: %w", resource, name, err)
		}
	}
	return nil
}

func (k *KubeOps) InspectContainer(ctx context.Context, name string) (*docker.ContainerInfo, error) {
	var pod Pod
	if err := k.client.Get(ctx, "pods", name, &pod); err != nil {
		if IsNotFound(err) {
			return nil, fmt.Errorf("pod %q not found", name)
		}
		return nil, err
	}

	state := strings.ToLower(string(pod.Status.Phase))
	status := state

	var startedAt time.Time
	if len(pod.Status.ContainerStatuses) > 0 {
		cs := pod.Status.ContainerStatuses[0]
		if cs.State.Running != nil && cs.State.Running.StartedAt != "" {
			startedAt, _ = time.Parse(time.RFC3339, cs.State.Running.StartedAt)
		}
		if cs.State.Waiting != nil {
			status = cs.State.Waiting.Reason
		}
	}

	return &docker.ContainerInfo{
		Name:      pod.Metadata.Name,
		ID:        pod.Metadata.Name,
		Image:     k.image,
		State:     state,
		Status:    status,
		StartedAt: startedAt,
	}, nil
}

func (k *KubeOps) ListContainers(ctx context.Context, prefix string) ([]docker.ContainerInfo, error) {
	var list PodList
	if err := k.client.List(ctx, "pods", k.instanceLabels(), &list); err != nil {
		return nil, err
	}

	var result []docker.ContainerInfo
	for _, pod := range list.Items {
		if prefix != "" && !strings.HasPrefix(pod.Metadata.Name, prefix) {
			continue
		}
		state := strings.ToLower(string(pod.Status.Phase))
		result = append(result, docker.ContainerInfo{
			Name:  pod.Metadata.Name,
			ID:    pod.Metadata.Name,
			Image: k.image,
			State: state,
		})
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Port mapping
// ---------------------------------------------------------------------------

func (k *KubeOps) MappedPort(_ context.Context, name string) (int, error) {
	// In K8s, routing goes through the Service DNS name at the gateway port.
	// The mux uses the port to construct the webhook URL; for K8s the URL
	// uses the Service DNS name directly, so we return the container port.
	return gatewayPort, nil
}

func (k *KubeOps) WebPort(_ context.Context, _ string) (int, error) {
	return webChannelPort, nil
}

// webChannelPort is the port for the nullclaw web channel WebSocket server.
const webChannelPort = 32123

// ---------------------------------------------------------------------------
// Container interaction
// ---------------------------------------------------------------------------

func (k *KubeOps) ContainerLogs(ctx context.Context, name string, opts docker.LogOpts) (io.ReadCloser, error) {
	lines := 100
	switch {
	case opts.Tail == "all":
		lines = 10000
	case opts.Tail != "":
		if n, err := strconv.Atoi(opts.Tail); err == nil && n > 0 {
			lines = n
		}
	}
	return k.client.PodLogs(ctx, name, lines)
}

func (k *KubeOps) ExecSync(ctx context.Context, name string, cmd []string, _ io.Reader) (string, error) {
	return k.client.Exec(ctx, name, cmd)
}

func (k *KubeOps) ExecFire(ctx context.Context, name string, cmd []string, _ io.Reader) error {
	_, err := k.client.Exec(ctx, name, cmd)
	return err
}

func (k *KubeOps) AttachInteractive(_ context.Context, _ string, _ []string) error {
	return docker.ErrNotSupported
}

// ---------------------------------------------------------------------------
// File transfer via exec+tar
// ---------------------------------------------------------------------------

// maxCopySize limits file transfer payloads to prevent OOM (matches tools/files.go).
const maxCopySize = 50 << 20 // 50MB

func (k *KubeOps) CopyToContainer(ctx context.Context, name, destPath string, content io.Reader) error {
	// content is a tar archive (same contract as Docker).
	// Pipe it to: tar xf - -C <destPath>
	data, err := io.ReadAll(io.LimitReader(content, maxCopySize+1))
	if err != nil {
		return fmt.Errorf("kube: read tar content: %w", err)
	}
	if len(data) > maxCopySize {
		return fmt.Errorf("kube: tar content too large (>%d bytes)", maxCopySize)
	}

	cmd := []string{"tar", "xf", "-", "-C", destPath}
	_, err = k.client.ExecWithStdin(ctx, name, cmd, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("kube: copy to %s:%s: %w", name, destPath, err)
	}
	return nil
}

func (k *KubeOps) CopyFromContainer(ctx context.Context, name, srcPath string) (io.ReadCloser, error) {
	// Run: tar cf - -C <dir> <base>
	dir := filepath.Dir(srcPath)
	base := filepath.Base(srcPath)

	stdout, err := k.client.Exec(ctx, name, []string{"tar", "cf", "-", "-C", dir, base})
	if err != nil {
		return nil, fmt.Errorf("kube: copy from %s:%s: %w", name, srcPath, err)
	}

	// Go strings hold arbitrary bytes — []byte(stdout) round-trips correctly.
	return io.NopCloser(bytes.NewReader([]byte(stdout))), nil
}

// ---------------------------------------------------------------------------
// Image management (no-ops — images are managed externally in K8s)
// ---------------------------------------------------------------------------

func (k *KubeOps) ImageExists(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func (k *KubeOps) ImageInspect(_ context.Context, image string) (*docker.ImageInfo, error) {
	return &docker.ImageInfo{Tags: []string{image}}, nil
}

func (k *KubeOps) ImagePull(_ context.Context, _ string) error {
	return nil // K8s handles image pulling via imagePullPolicy
}

func (k *KubeOps) ImageTag(_ context.Context, _, _ string) error {
	return docker.ErrNotSupported
}

func (k *KubeOps) ImageBuild(_ context.Context, _ string, _ docker.BuildOpts) error {
	return docker.ErrNotSupported
}

// ---------------------------------------------------------------------------
// Networking (no-ops — K8s networking is implicit)
// ---------------------------------------------------------------------------

func (k *KubeOps) EnsureNetwork(_ context.Context, _ string) error {
	return nil
}

func (k *KubeOps) ConnectNetwork(_ context.Context, _, _ string) error {
	return nil
}

// ---------------------------------------------------------------------------
// Cleanup
// ---------------------------------------------------------------------------

func (k *KubeOps) Close() error {
	return nil
}
