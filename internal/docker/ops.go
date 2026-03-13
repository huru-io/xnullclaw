// Package docker provides a Docker SDK facade.
// This is the ONLY package in the project that imports the Docker SDK.
// All other packages depend on the Ops interface for mocking.
package docker

import (
	"context"
	"io"
	"time"
)

// Ops defines all Docker operations used by xnc.
type Ops interface {
	// Container lifecycle
	IsRunning(ctx context.Context, name string) (bool, error)
	StartContainer(ctx context.Context, name string, opts ContainerOpts) error
	StopContainer(ctx context.Context, name string) error
	RemoveContainer(ctx context.Context, name string, force bool) error
	InspectContainer(ctx context.Context, name string) (*ContainerInfo, error)
	ListContainers(ctx context.Context, prefix string) ([]ContainerInfo, error)

	// Port mapping
	MappedPort(ctx context.Context, name string) (int, error)

	// Container interaction
	ContainerLogs(ctx context.Context, name string, opts LogOpts) (io.ReadCloser, error)
	ExecSync(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error)
	ExecFire(ctx context.Context, name string, cmd []string, stdin io.Reader) error
	AttachInteractive(ctx context.Context, name string, cmd []string) error

	// File transfer
	CopyToContainer(ctx context.Context, name, destPath string, content io.Reader) error
	CopyFromContainer(ctx context.Context, name, srcPath string) (io.ReadCloser, error)

	// Image management
	ImageExists(ctx context.Context, image string) (bool, error)
	ImageInspect(ctx context.Context, image string) (*ImageInfo, error)
	ImagePull(ctx context.Context, refStr string) error
	ImageTag(ctx context.Context, source, target string) error
	ImageBuild(ctx context.Context, contextDir string, opts BuildOpts) error

	// Cleanup
	Close() error
}

// ContainerOpts configures container creation.
type ContainerOpts struct {
	Image      string
	Cmd        []string
	AgentDir   string // host path to agent directory (for mounts)
	ExposePort bool   // expose gateway port (Docker auto-assigns host port)
	Env        []string
	TTY        bool // interactive mode
}

// ContainerInfo holds container inspection data.
type ContainerInfo struct {
	Name      string    `json:"name"`
	ID        string    `json:"id"`
	Image     string    `json:"image"`
	State     string    `json:"state"`  // running, exited, etc.
	Status    string    `json:"status"` // human-readable status
	StartedAt time.Time `json:"started_at"`
	Ports     []string  `json:"ports,omitempty"`
}

// LogOpts configures container log retrieval.
type LogOpts struct {
	Follow bool
	Tail   string // "all", "100", etc.
	Since  string // timestamp or duration
}

// BuildOpts configures Docker image builds.
type BuildOpts struct {
	Tags    []string
	NoCache bool
}

// ImageInfo holds image inspection data.
type ImageInfo struct {
	ID      string    `json:"id"`
	Tags    []string  `json:"tags"`
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
}
