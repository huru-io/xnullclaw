package agent

// LocalBackend implements Backend using the local filesystem.
// It delegates to the existing agent.* package functions with
// the Home parameter baked in. No behavior change from direct calls.
type LocalBackend struct {
	Home string
}

func (b *LocalBackend) Setup(name string, opts SetupOpts) error {
	return Setup(b.Home, name, opts)
}

func (b *LocalBackend) Destroy(name string) error {
	return Destroy(b.Home, name)
}

func (b *LocalBackend) Exists(name string) bool {
	return Exists(b.Home, name)
}

func (b *LocalBackend) ListAll() ([]Info, error) {
	return ListAll(b.Home)
}

func (b *LocalBackend) Clone(source, dest string, opts CloneOpts) error {
	return Clone(b.Home, source, dest, opts)
}

func (b *LocalBackend) Rename(oldName, newName string) error {
	return Rename(b.Home, oldName, newName)
}

func (b *LocalBackend) ConfigGet(name, key string) (any, error) {
	return ConfigGet(Dir(b.Home, name), key)
}

func (b *LocalBackend) ConfigSet(name, key, value string) error {
	return ConfigSet(Dir(b.Home, name), key, value)
}

func (b *LocalBackend) ConfigGetAll(name string) (map[string]any, error) {
	return ConfigGetAll(Dir(b.Home, name))
}

func (b *LocalBackend) ConfigGetAllRedacted(name string) (map[string]any, error) {
	return ConfigGetAllRedacted(Dir(b.Home, name))
}

func (b *LocalBackend) ReadMeta(name string) (map[string]string, error) {
	return ReadMeta(Dir(b.Home, name))
}

func (b *LocalBackend) WriteMeta(name, key, value string) error {
	return WriteMeta(Dir(b.Home, name), key, value)
}

func (b *LocalBackend) WriteMetaBatch(name string, pairs map[string]string) error {
	return WriteMetaBatch(Dir(b.Home, name), pairs)
}

func (b *LocalBackend) ReadToken(name string) (string, error) {
	return ReadToken(Dir(b.Home, name))
}

func (b *LocalBackend) SetupWebhookAuth(name string) (string, error) {
	return SetupWebhookAuth(Dir(b.Home, name))
}

func (b *LocalBackend) HasProviderKey(name string) bool {
	return HasProviderKey(b.Home, name)
}

func (b *LocalBackend) CollectKeys() map[string]string {
	return CollectKeys(b.Home)
}

func (b *LocalBackend) ContainerEnv(name string) ([]string, error) {
	return ContainerEnv(Dir(b.Home, name)), nil
}

func (b *LocalBackend) Dir(name string) string {
	return Dir(b.Home, name)
}
