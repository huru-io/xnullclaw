package agent

// Backend abstracts agent state management. LocalBackend wraps the existing
// filesystem functions. KubeBackend (internal/kube) stores state in K8s
// resources. The mux uses Backend for all agent operations; the CLI always
// uses LocalBackend (filesystem) directly.
type Backend interface {
	// CRUD
	Setup(name string, opts SetupOpts) error
	Destroy(name string) error
	Exists(name string) bool
	ListAll() ([]Info, error)
	Clone(source, dest string, opts CloneOpts) error
	Rename(oldName, newName string) error

	// Config — reads/writes the agent's config.json
	ConfigGet(name, key string) (any, error)
	ConfigSet(name, key, value string) error
	ConfigGetAll(name string) (map[string]any, error)
	ConfigGetAllRedacted(name string) (map[string]any, error)

	// Metadata — reads/writes the agent's .meta key=value file
	ReadMeta(name string) (map[string]string, error)
	WriteMeta(name, key, value string) error
	WriteMetaBatch(name string, pairs map[string]string) error

	// Auth — bearer tokens for webhook authentication
	ReadToken(name string) (string, error)
	SetupWebhookAuth(name string) (string, error)

	// Provider keys
	HasProviderKey(name string) bool
	CollectKeys() map[string]string

	// Container support
	ContainerEnv(name string) ([]string, error)

	// Dir returns the filesystem path for the agent (local/docker mode)
	// or empty string (kubernetes mode where there is no local directory).
	Dir(name string) string
}
