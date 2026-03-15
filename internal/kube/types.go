package kube

// Minimal K8s resource structs — only the fields xnc needs.
// Omitted fields are preserved through json.RawMessage round-tripping where needed.

// ObjectMeta contains standard metadata for K8s resources.
type ObjectMeta struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
}

// Pod represents a K8s Pod (minimal fields).
type Pod struct {
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PodSpec   `json:"spec"`
	Status     PodStatus `json:"status,omitempty"`
}

// PodSpec is the specification of a Pod.
type PodSpec struct {
	Containers                    []Container        `json:"containers"`
	Volumes                       []Volume           `json:"volumes,omitempty"`
	RestartPolicy                 string             `json:"restartPolicy,omitempty"`
	SecurityContext               *PodSecurityContext `json:"securityContext,omitempty"`
	ServiceAccountName            string             `json:"serviceAccountName,omitempty"`
	AutomountServiceAccountToken  *bool              `json:"automountServiceAccountToken,omitempty"`
	NodeSelector                  map[string]string  `json:"nodeSelector,omitempty"`
}

// Container describes a single container in a Pod.
type Container struct {
	Name            string               `json:"name"`
	Image           string               `json:"image"`
	ImagePullPolicy string               `json:"imagePullPolicy,omitempty"`
	Command         []string             `json:"command,omitempty"`
	Args            []string             `json:"args,omitempty"`
	Env             []EnvVar             `json:"env,omitempty"`
	Ports           []ContainerPort      `json:"ports,omitempty"`
	VolumeMounts    []VolumeMount        `json:"volumeMounts,omitempty"`
	Resources       ResourceRequirements `json:"resources,omitempty"`
	SecurityContext *SecurityContext      `json:"securityContext,omitempty"`
}

// EnvVar is a single environment variable.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

// ContainerPort is a port exposed by a container.
type ContainerPort struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

// VolumeMount describes a volume mount in a container.
type VolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// Volume describes a pod volume.
type Volume struct {
	Name                  string                `json:"name"`
	PersistentVolumeClaim *PVCVolumeSource      `json:"persistentVolumeClaim,omitempty"`
	EmptyDir              *EmptyDirVolumeSource `json:"emptyDir,omitempty"`
}

// PVCVolumeSource references a PVC.
type PVCVolumeSource struct {
	ClaimName string `json:"claimName"`
}

// EmptyDirVolumeSource is an emptyDir volume.
type EmptyDirVolumeSource struct {
	Medium    string `json:"medium,omitempty"`
	SizeLimit string `json:"sizeLimit,omitempty"`
}

// ResourceRequirements describes compute resource requests/limits.
type ResourceRequirements struct {
	Limits   map[string]string `json:"limits,omitempty"`
	Requests map[string]string `json:"requests,omitempty"`
}

// SecurityContext holds security settings for a container.
type SecurityContext struct {
	RunAsNonRoot             *bool          `json:"runAsNonRoot,omitempty"`
	RunAsUser                *int64         `json:"runAsUser,omitempty"`
	RunAsGroup               *int64         `json:"runAsGroup,omitempty"`
	ReadOnlyRootFilesystem   *bool          `json:"readOnlyRootFilesystem,omitempty"`
	AllowPrivilegeEscalation *bool          `json:"allowPrivilegeEscalation,omitempty"`
	Capabilities             *Capabilities  `json:"capabilities,omitempty"`
	SeccompProfile           *SeccompProfile `json:"seccompProfile,omitempty"`
}

// PodSecurityContext holds pod-level security settings.
type PodSecurityContext struct {
	RunAsNonRoot   *bool           `json:"runAsNonRoot,omitempty"`
	RunAsUser      *int64          `json:"runAsUser,omitempty"`
	RunAsGroup     *int64          `json:"runAsGroup,omitempty"`
	FSGroup        *int64          `json:"fsGroup,omitempty"`
	SeccompProfile *SeccompProfile `json:"seccompProfile,omitempty"`
}

// SeccompProfile defines the seccomp profile for a pod or container.
type SeccompProfile struct {
	Type string `json:"type"`
}

// Capabilities holds Linux capabilities to add/drop.
type Capabilities struct {
	Add  []string `json:"add,omitempty"`
	Drop []string `json:"drop,omitempty"`
}

// PodStatus is the observed state of a Pod.
type PodStatus struct {
	Phase             string             `json:"phase,omitempty"`
	PodIP             string             `json:"podIP,omitempty"`
	ContainerStatuses []ContainerStatus  `json:"containerStatuses,omitempty"`
}

// ContainerStatus is the status of a container.
type ContainerStatus struct {
	Name         string         `json:"name"`
	Ready        bool           `json:"ready"`
	RestartCount int            `json:"restartCount"`
	State        ContainerState `json:"state,omitempty"`
}

// ContainerState holds the state of a container.
type ContainerState struct {
	Waiting    *ContainerStateWaiting    `json:"waiting,omitempty"`
	Running    *ContainerStateRunning    `json:"running,omitempty"`
	Terminated *ContainerStateTerminated `json:"terminated,omitempty"`
}

// ContainerStateWaiting represents a container that is not yet running.
type ContainerStateWaiting struct {
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// ContainerStateRunning represents a running container.
type ContainerStateRunning struct {
	StartedAt string `json:"startedAt,omitempty"`
}

// ContainerStateTerminated represents a terminated container.
type ContainerStateTerminated struct {
	ExitCode int    `json:"exitCode"`
	Reason   string `json:"reason,omitempty"`
}

// PodList is a list of Pods.
type PodList struct {
	Items []Pod `json:"items"`
}

// Service represents a K8s Service (minimal fields).
type Service struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   ObjectMeta  `json:"metadata"`
	Spec       ServiceSpec `json:"spec"`
}

// ServiceSpec is the specification of a Service.
type ServiceSpec struct {
	Type     string            `json:"type,omitempty"`
	Selector map[string]string `json:"selector,omitempty"`
	Ports    []ServicePort     `json:"ports,omitempty"`
}

// ServicePort is a port exposed by a Service.
type ServicePort struct {
	Name       string `json:"name,omitempty"`
	Port       int    `json:"port"`
	TargetPort int    `json:"targetPort,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
}

// ServiceList is a list of Services.
type ServiceList struct {
	Items []Service `json:"items"`
}

// ConfigMap represents a K8s ConfigMap (minimal fields).
type ConfigMap struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   ObjectMeta        `json:"metadata"`
	Data       map[string]string `json:"data,omitempty"`
}

// ConfigMapList is a list of ConfigMaps.
type ConfigMapList struct {
	Items []ConfigMap `json:"items"`
}

// Secret represents a K8s Secret (minimal fields).
type Secret struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   ObjectMeta        `json:"metadata"`
	Type       string            `json:"type,omitempty"`
	StringData map[string]string `json:"stringData,omitempty"`
	Data       map[string]string `json:"data,omitempty"` // base64-encoded by K8s API
}

// SecretList is a list of Secrets.
type SecretList struct {
	Items []Secret `json:"items"`
}

// PersistentVolumeClaim represents a K8s PVC (minimal fields).
type PersistentVolumeClaim struct {
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PVCSpec   `json:"spec"`
	Status     PVCStatus `json:"status,omitempty"`
}

// PVCSpec is the specification of a PVC.
type PVCSpec struct {
	AccessModes []string                    `json:"accessModes,omitempty"`
	Resources   PVCResourceRequirements     `json:"resources,omitempty"`
}

// PVCResourceRequirements describes storage resource requests.
type PVCResourceRequirements struct {
	Requests map[string]string `json:"requests,omitempty"`
}

// PVCStatus is the observed state of a PVC.
type PVCStatus struct {
	Phase string `json:"phase,omitempty"`
}

// PVCList is a list of PVCs.
type PVCList struct {
	Items []PersistentVolumeClaim `json:"items"`
}
