package kube

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jotavich/xnullclaw/internal/docker"
)

// gatewayPort is the port nullclaw listens on inside the container.
const gatewayPort = 3000

// KubeOps implements docker.Ops by managing K8s Pods, Services, etc.
type KubeOps struct {
	client     *Client
	instanceID string // 6-hex-char instance ID for resource naming
	image      string // default container image
}

var _ docker.Ops = (*KubeOps)(nil)

// NewOps creates a KubeOps adapter.
func NewOps(client *Client, instanceID, image string) *KubeOps {
	return &KubeOps{
		client:     client,
		instanceID: instanceID,
		image:      image,
	}
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

	// Create PVC for agent data.
	pvc := PersistentVolumeClaim{
		APIVersion: "v1",
		Kind:       "PersistentVolumeClaim",
		Metadata:   ObjectMeta{Name: name, Labels: labels},
		Spec: PVCSpec{
			AccessModes: []string{"ReadWriteOnce"},
			Resources:   PVCResourceRequirements{Requests: map[string]string{"storage": "1Gi"}},
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
			RestartPolicy: "Always",
			SecurityContext: &PodSecurityContext{
				RunAsNonRoot: &tt,
			},
			Containers: []Container{{
				Name:    "agent",
				Image:   opts.Image,
				Command: opts.Cmd,
				Env:     envVars,
				Ports: []ContainerPort{{
					Name:          "http",
					ContainerPort: gatewayPort,
				}},
				VolumeMounts: []VolumeMount{
					{Name: "data", MountPath: "/nullclaw-data"},
					{Name: "tmp", MountPath: "/tmp"},
				},
				Resources: ResourceRequirements{
					Requests: map[string]string{
						"memory": "64Mi",
						"cpu":    "100m",
					},
					Limits: map[string]string{
						"memory": "128Mi",
						"cpu":    "250m",
					},
				},
				SecurityContext: &SecurityContext{
					ReadOnlyRootFilesystem:   &tt,
					AllowPrivilegeEscalation: &f,
					Capabilities:             &Capabilities{Drop: []string{"ALL"}},
				},
			}},
			Volumes: []Volume{
				{
					Name:                  "data",
					PersistentVolumeClaim: &PVCVolumeSource{ClaimName: name},
				},
				{
					Name:     "tmp",
					EmptyDir: &EmptyDirVolumeSource{SizeLimit: "64Mi"},
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
				Ports: []ServicePort{{
					Name:       "http",
					Port:       gatewayPort,
					TargetPort: gatewayPort,
				}},
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
	return nil
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

// ---------------------------------------------------------------------------
// Container interaction
// ---------------------------------------------------------------------------

func (k *KubeOps) ContainerLogs(ctx context.Context, name string, opts docker.LogOpts) (io.ReadCloser, error) {
	lines := 100
	if opts.Tail == "all" {
		lines = 10000
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
// File transfer (not supported in K8s mode)
// ---------------------------------------------------------------------------

func (k *KubeOps) CopyToContainer(_ context.Context, _, _ string, _ io.Reader) error {
	return docker.ErrNotSupported
}

func (k *KubeOps) CopyFromContainer(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return nil, docker.ErrNotSupported
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
