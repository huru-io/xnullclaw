package agent

// MockBackend is a test double for the Backend interface.
// Each method delegates to a function field if set, otherwise returns a sensible default.
type MockBackend struct {
	SetupFn              func(name string, opts SetupOpts) error
	DestroyFn            func(name string) error
	ExistsFn             func(name string) bool
	ListAllFn            func() ([]Info, error)
	CloneFn              func(source, dest string, opts CloneOpts) error
	RenameFn             func(oldName, newName string) error
	ConfigGetFn          func(name, key string) (any, error)
	ConfigSetFn          func(name, key, value string) error
	ConfigGetAllFn       func(name string) (map[string]any, error)
	ConfigGetAllRedactFn func(name string) (map[string]any, error)
	ReadMetaFn           func(name string) (map[string]string, error)
	WriteMetaFn          func(name, key, value string) error
	WriteMetaBatchFn     func(name string, pairs map[string]string) error
	ReadTokenFn          func(name string) (string, error)
	SetupWebhookAuthFn   func(name string) (string, error)
	HasProviderKeyFn     func(name string) bool
	CollectKeysFn        func() map[string]string
	ContainerEnvFn       func(name string) ([]string, error)
	DirFn                func(name string) string
}

var _ Backend = (*MockBackend)(nil)

func (m *MockBackend) Setup(name string, opts SetupOpts) error {
	if m.SetupFn != nil {
		return m.SetupFn(name, opts)
	}
	return nil
}

func (m *MockBackend) Destroy(name string) error {
	if m.DestroyFn != nil {
		return m.DestroyFn(name)
	}
	return nil
}

func (m *MockBackend) Exists(name string) bool {
	if m.ExistsFn != nil {
		return m.ExistsFn(name)
	}
	return false
}

func (m *MockBackend) ListAll() ([]Info, error) {
	if m.ListAllFn != nil {
		return m.ListAllFn()
	}
	return nil, nil
}

func (m *MockBackend) Clone(source, dest string, opts CloneOpts) error {
	if m.CloneFn != nil {
		return m.CloneFn(source, dest, opts)
	}
	return nil
}

func (m *MockBackend) Rename(oldName, newName string) error {
	if m.RenameFn != nil {
		return m.RenameFn(oldName, newName)
	}
	return nil
}

func (m *MockBackend) ConfigGet(name, key string) (any, error) {
	if m.ConfigGetFn != nil {
		return m.ConfigGetFn(name, key)
	}
	return nil, nil
}

func (m *MockBackend) ConfigSet(name, key, value string) error {
	if m.ConfigSetFn != nil {
		return m.ConfigSetFn(name, key, value)
	}
	return nil
}

func (m *MockBackend) ConfigGetAll(name string) (map[string]any, error) {
	if m.ConfigGetAllFn != nil {
		return m.ConfigGetAllFn(name)
	}
	return map[string]any{}, nil
}

func (m *MockBackend) ConfigGetAllRedacted(name string) (map[string]any, error) {
	if m.ConfigGetAllRedactFn != nil {
		return m.ConfigGetAllRedactFn(name)
	}
	return map[string]any{}, nil
}

func (m *MockBackend) ReadMeta(name string) (map[string]string, error) {
	if m.ReadMetaFn != nil {
		return m.ReadMetaFn(name)
	}
	return map[string]string{}, nil
}

func (m *MockBackend) WriteMeta(name, key, value string) error {
	if m.WriteMetaFn != nil {
		return m.WriteMetaFn(name, key, value)
	}
	return nil
}

func (m *MockBackend) WriteMetaBatch(name string, pairs map[string]string) error {
	if m.WriteMetaBatchFn != nil {
		return m.WriteMetaBatchFn(name, pairs)
	}
	return nil
}

func (m *MockBackend) ReadToken(name string) (string, error) {
	if m.ReadTokenFn != nil {
		return m.ReadTokenFn(name)
	}
	return "", nil
}

func (m *MockBackend) SetupWebhookAuth(name string) (string, error) {
	if m.SetupWebhookAuthFn != nil {
		return m.SetupWebhookAuthFn(name)
	}
	return "mock-token", nil
}

func (m *MockBackend) HasProviderKey(name string) bool {
	if m.HasProviderKeyFn != nil {
		return m.HasProviderKeyFn(name)
	}
	return true
}

func (m *MockBackend) CollectKeys() map[string]string {
	if m.CollectKeysFn != nil {
		return m.CollectKeysFn()
	}
	return map[string]string{}
}

func (m *MockBackend) ContainerEnv(name string) ([]string, error) {
	if m.ContainerEnvFn != nil {
		return m.ContainerEnvFn(name)
	}
	return nil, nil
}

func (m *MockBackend) Dir(name string) string {
	if m.DirFn != nil {
		return m.DirFn(name)
	}
	return ""
}
