package docker

import (
	"context"
	"fmt"
	"io"
)

// MockOps is a test double for the Ops interface.
// Each method delegates to a function field if set, otherwise returns a sensible default.
type MockOps struct {
	IsRunningFn          func(ctx context.Context, name string) (bool, error)
	StartContainerFn     func(ctx context.Context, name string, opts ContainerOpts) error
	StopContainerFn      func(ctx context.Context, name string) error
	RemoveContainerFn    func(ctx context.Context, name string, force bool) error
	InspectContainerFn   func(ctx context.Context, name string) (*ContainerInfo, error)
	ListContainersFn     func(ctx context.Context, prefix string) ([]ContainerInfo, error)
	MappedPortFn         func(ctx context.Context, name string) (int, error)
	ContainerLogsFn      func(ctx context.Context, name string, opts LogOpts) (io.ReadCloser, error)
	ExecSyncFn           func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error)
	ExecFireFn           func(ctx context.Context, name string, cmd []string, stdin io.Reader) error
	AttachInteractiveFn  func(ctx context.Context, name string, cmd []string) error
	CopyToContainerFn    func(ctx context.Context, name, destPath string, content io.Reader) error
	CopyFromContainerFn  func(ctx context.Context, name, srcPath string) (io.ReadCloser, error)
	ImageExistsFn        func(ctx context.Context, image string) (bool, error)
	ImageInspectFn       func(ctx context.Context, image string) (*ImageInfo, error)
	ImagePullFn          func(ctx context.Context, refStr string) error
	ImageTagFn           func(ctx context.Context, source, target string) error
	ImageBuildFn         func(ctx context.Context, contextDir string, opts BuildOpts) error
	CloseFn              func() error
}

var _ Ops = (*MockOps)(nil)

func (m *MockOps) IsRunning(ctx context.Context, name string) (bool, error) {
	if m.IsRunningFn != nil {
		return m.IsRunningFn(ctx, name)
	}
	return false, nil
}

func (m *MockOps) StartContainer(ctx context.Context, name string, opts ContainerOpts) error {
	if m.StartContainerFn != nil {
		return m.StartContainerFn(ctx, name, opts)
	}
	return nil
}

func (m *MockOps) StopContainer(ctx context.Context, name string) error {
	if m.StopContainerFn != nil {
		return m.StopContainerFn(ctx, name)
	}
	return nil
}

func (m *MockOps) RemoveContainer(ctx context.Context, name string, force bool) error {
	if m.RemoveContainerFn != nil {
		return m.RemoveContainerFn(ctx, name, force)
	}
	return nil
}

func (m *MockOps) InspectContainer(ctx context.Context, name string) (*ContainerInfo, error) {
	if m.InspectContainerFn != nil {
		return m.InspectContainerFn(ctx, name)
	}
	return nil, fmt.Errorf("No such container: %s", name)
}

func (m *MockOps) ListContainers(ctx context.Context, prefix string) ([]ContainerInfo, error) {
	if m.ListContainersFn != nil {
		return m.ListContainersFn(ctx, prefix)
	}
	return nil, nil
}

func (m *MockOps) MappedPort(ctx context.Context, name string) (int, error) {
	if m.MappedPortFn != nil {
		return m.MappedPortFn(ctx, name)
	}
	return 0, nil
}

func (m *MockOps) ContainerLogs(ctx context.Context, name string, opts LogOpts) (io.ReadCloser, error) {
	if m.ContainerLogsFn != nil {
		return m.ContainerLogsFn(ctx, name, opts)
	}
	return io.NopCloser(io.LimitReader(nil, 0)), nil
}

func (m *MockOps) ExecSync(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
	if m.ExecSyncFn != nil {
		return m.ExecSyncFn(ctx, name, cmd, stdin)
	}
	return "", nil
}

func (m *MockOps) ExecFire(ctx context.Context, name string, cmd []string, stdin io.Reader) error {
	if m.ExecFireFn != nil {
		return m.ExecFireFn(ctx, name, cmd, stdin)
	}
	return nil
}

func (m *MockOps) AttachInteractive(ctx context.Context, name string, cmd []string) error {
	if m.AttachInteractiveFn != nil {
		return m.AttachInteractiveFn(ctx, name, cmd)
	}
	return nil
}

func (m *MockOps) CopyToContainer(ctx context.Context, name, destPath string, content io.Reader) error {
	if m.CopyToContainerFn != nil {
		return m.CopyToContainerFn(ctx, name, destPath, content)
	}
	return nil
}

func (m *MockOps) CopyFromContainer(ctx context.Context, name, srcPath string) (io.ReadCloser, error) {
	if m.CopyFromContainerFn != nil {
		return m.CopyFromContainerFn(ctx, name, srcPath)
	}
	return io.NopCloser(io.LimitReader(nil, 0)), nil
}

func (m *MockOps) ImageExists(ctx context.Context, image string) (bool, error) {
	if m.ImageExistsFn != nil {
		return m.ImageExistsFn(ctx, image)
	}
	return false, nil
}

func (m *MockOps) ImageInspect(ctx context.Context, image string) (*ImageInfo, error) {
	if m.ImageInspectFn != nil {
		return m.ImageInspectFn(ctx, image)
	}
	return nil, fmt.Errorf("No such image: %s", image)
}

func (m *MockOps) ImagePull(ctx context.Context, refStr string) error {
	if m.ImagePullFn != nil {
		return m.ImagePullFn(ctx, refStr)
	}
	return nil
}

func (m *MockOps) ImageTag(ctx context.Context, source, target string) error {
	if m.ImageTagFn != nil {
		return m.ImageTagFn(ctx, source, target)
	}
	return nil
}

func (m *MockOps) ImageBuild(ctx context.Context, contextDir string, opts BuildOpts) error {
	if m.ImageBuildFn != nil {
		return m.ImageBuildFn(ctx, contextDir, opts)
	}
	return nil
}

func (m *MockOps) Close() error {
	if m.CloseFn != nil {
		return m.CloseFn()
	}
	return nil
}
