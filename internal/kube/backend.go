package kube

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
)

// validMetaKey matches safe K8s annotation name segments.
var validMetaKey = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// maxMetaValueSize limits individual annotation values to 128KB.
// K8s has a 256KB total annotation limit; this prevents a single value from
// consuming the budget and leaves room for system annotations.
const maxMetaValueSize = 128 * 1024

// decodeSecretValue decodes a base64-encoded value from Secret.Data.
// K8s API returns Secret.Data values as base64; Secret.StringData is write-only.
// Falls back to raw value if decode fails (e.g., test mocks using StringData).
func decodeSecretValue(encoded string) string {
	if encoded == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		// Non-base64 data in Secret.Data — unusual in production K8s.
		// Return as-is; this path is expected in tests using mock stores.
		return encoded
	}
	return string(decoded)
}

// isSecretStoredKey returns true if this config key is stored in the K8s Secret
// (as opposed to the ConfigMap). Provider API keys and keys with EnvVar mappings
// are stored in Secrets; other redacted keys (telegram_token, gateway_paired_tokens)
// are stored in the ConfigMap's config.json.
func isSecretStoredKey(ck agent.ConfigKey) bool {
	return ck.Redacted && (ck.Provider != "" || ck.EnvVar != "")
}

// apiTimeout is the default timeout for KubeBackend operations.
// Prevents indefinite stalls on unresponsive K8s API servers.
const apiTimeout = 30 * time.Second

// KubeBackend implements agent.Backend using K8s ConfigMaps and Secrets.
// Agent state is stored as:
//   - ConfigMap: config.json data + metadata annotations
//   - Secret: API keys + auth tokens
type KubeBackend struct {
	client     *Client
	instanceID string
}

// ctx returns a context with the standard API timeout.
// Used by Backend interface methods that don't receive a caller context.
func (b *KubeBackend) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), apiTimeout)
}

var _ agent.Backend = (*KubeBackend)(nil)

// NewBackend creates a KubeBackend.
func NewBackend(client *Client, instanceID string) *KubeBackend {
	return &KubeBackend{
		client:     client,
		instanceID: instanceID,
	}
}

// resourceName returns the K8s resource name for an agent.
func (b *KubeBackend) resourceName(name string) string {
	return "xnc-" + b.instanceID + "-" + agent.CanonicalName(name)
}

// labels returns standard labels for agent resources.
func (b *KubeBackend) labels(name string) map[string]string {
	return agentLabels(b.instanceID, name)
}

// instanceLabels returns labels matching all resources for this xnc instance
// (without agent-specific filtering).
func (b *KubeBackend) instanceLabels() map[string]string {
	return instanceLabelsFor(b.instanceID)
}

// ---------------------------------------------------------------------------
// CRUD
// ---------------------------------------------------------------------------

func (b *KubeBackend) Setup(name string, opts agent.SetupOpts) error {
	if err := agent.ValidateName(name); err != nil {
		return err
	}

	ctx, cancel := b.ctx()
	defer cancel()
	resName := b.resourceName(name)
	labels := b.labels(name)

	// Build config data (mirrors agent.Setup's config.json).
	cfg := map[string]any{}
	if opts.Model != "" {
		setNestedPath(cfg, "agents.defaults.model.primary", opts.Model)
	}
	if opts.SystemPrompt != "" {
		setNestedPath(cfg, "agents.defaults.system_prompt", opts.SystemPrompt)
	}
	if len(opts.TelegramAllow) > 0 {
		setNestedPath(cfg, "channels.telegram.accounts.main.allow_from", opts.TelegramAllow)
	}

	cfgJSON, _ := json.Marshal(cfg)

	// Create ConfigMap with config.json.
	cm := ConfigMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata: ObjectMeta{
			Name:   resName,
			Labels: labels,
			Annotations: map[string]string{
				"xnc.io/agent-name": agent.CanonicalName(name),
				"xnc.io/created":    time.Now().UTC().Format(time.RFC3339),
			},
		},
		Data: map[string]string{
			"config.json": string(cfgJSON),
		},
	}
	if err := b.client.Create(ctx, "configmaps", cm, nil); err != nil {
		return fmt.Errorf("create configmap: %w", err)
	}

	// Create Secret with API keys.
	secretData := map[string]string{}
	if opts.OpenAIKey != "" {
		secretData["openai_key"] = opts.OpenAIKey
	}
	if opts.AnthropicKey != "" {
		secretData["anthropic_key"] = opts.AnthropicKey
	}
	if opts.OpenRouterKey != "" {
		secretData["openrouter_key"] = opts.OpenRouterKey
	}
	if opts.BraveKey != "" {
		secretData["brave_key"] = opts.BraveKey
	}

	secret := Secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata:   ObjectMeta{Name: resName, Labels: labels},
		Type:       "Opaque",
		StringData: secretData,
	}
	if err := b.client.Create(ctx, "secrets", secret, nil); err != nil {
		// Rollback: delete the orphaned ConfigMap.
		_ = b.client.Delete(ctx, "configmaps", resName)
		return fmt.Errorf("create secret: %w", err)
	}

	return nil
}

func (b *KubeBackend) Destroy(name string) error {
	ctx, cancel := b.ctx()
	defer cancel()
	resName := b.resourceName(name)

	for _, resource := range []string{"configmaps", "secrets"} {
		if err := b.client.Delete(ctx, resource, resName); err != nil && !IsNotFound(err) {
			return fmt.Errorf("delete %s: %w", resource, err)
		}
	}
	return nil
}

func (b *KubeBackend) Exists(name string) bool {
	ctx, cancel := b.ctx()
	defer cancel()
	var cm ConfigMap
	err := b.client.Get(ctx, "configmaps", b.resourceName(name), &cm)
	return err == nil
}

func (b *KubeBackend) ListAll() ([]agent.Info, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	var list ConfigMapList
	if err := b.client.List(ctx, "configmaps", b.instanceLabels(), &list); err != nil {
		return nil, err
	}

	// Filter: only include configmaps that belong to this instance.
	var result []agent.Info
	for _, cm := range list.Items {
		inst := cm.Metadata.Labels["xnc.io/instance"]
		if inst != b.instanceID {
			continue
		}
		agentName := cm.Metadata.Labels["xnc.io/agent"]
		if agentName == "" {
			agentName = cm.Metadata.Annotations["xnc.io/agent-name"]
		}
		created := cm.Metadata.Annotations["xnc.io/created"]
		result = append(result, agent.Info{
			Name:    agentName,
			Created: created,
		})
	}
	return result, nil
}

func (b *KubeBackend) Clone(source, dest string, opts agent.CloneOpts) error {
	if err := agent.ValidateName(dest); err != nil {
		return err
	}
	if b.Exists(dest) {
		return fmt.Errorf("agent %q already exists", dest)
	}

	ctx, cancel := b.ctx()
	defer cancel()
	srcName := b.resourceName(source)
	dstName := b.resourceName(dest)
	dstLabels := b.labels(dest)

	// Copy ConfigMap.
	var srcCM ConfigMap
	if err := b.client.Get(ctx, "configmaps", srcName, &srcCM); err != nil {
		return fmt.Errorf("read source config: %w", err)
	}
	// Preserve custom metadata annotations from source.
	dstAnnotations := map[string]string{
		"xnc.io/agent-name":  agent.CanonicalName(dest),
		"xnc.io/created":     time.Now().UTC().Format(time.RFC3339),
		"xnc.io/cloned-from": agent.CanonicalName(source),
	}
	for k, v := range srcCM.Metadata.Annotations {
		if strings.HasPrefix(k, "xnc.io/meta-") {
			dstAnnotations[k] = v
		}
	}

	dstCM := ConfigMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata: ObjectMeta{
			Name:        dstName,
			Labels:      dstLabels,
			Annotations: dstAnnotations,
		},
		Data: srcCM.Data,
	}
	if err := b.client.Create(ctx, "configmaps", dstCM, nil); err != nil {
		return fmt.Errorf("create dest config: %w", err)
	}

	// Copy Secret.
	var srcSecret Secret
	if err := b.client.Get(ctx, "secrets", srcName, &srcSecret); err != nil {
		if !IsNotFound(err) {
			// Rollback: delete the dest ConfigMap we just created.
			_ = b.client.Delete(ctx, "configmaps", dstName)
			return fmt.Errorf("read source secret: %w", err)
		}
		// No secret to copy — create empty.
		srcSecret.Data = map[string]string{}
	}
	dstSecret := Secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata:   ObjectMeta{Name: dstName, Labels: dstLabels},
		Type:       "Opaque",
		Data:       srcSecret.Data, // base64-encoded from API
	}
	if err := b.client.Create(ctx, "secrets", dstSecret, nil); err != nil {
		// Rollback: delete the dest ConfigMap we just created.
		_ = b.client.Delete(ctx, "configmaps", dstName)
		return fmt.Errorf("create dest secret: %w", err)
	}

	return nil
}

func (b *KubeBackend) Rename(oldName, newName string) error {
	// Clone then destroy old.
	if err := b.Clone(oldName, newName, agent.CloneOpts{}); err != nil {
		return err
	}
	return b.Destroy(oldName)
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

func (b *KubeBackend) configMap(name string) (*ConfigMap, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	var cm ConfigMap
	if err := b.client.Get(ctx, "configmaps", b.resourceName(name), &cm); err != nil {
		if IsNotFound(err) {
			return nil, fmt.Errorf("agent %q not found", name)
		}
		return nil, fmt.Errorf("get agent %q: %w", name, err)
	}
	return &cm, nil
}

func (b *KubeBackend) configData(name string) (map[string]any, error) {
	cm, err := b.configMap(name)
	if err != nil {
		return nil, err
	}
	raw := cm.Data["config.json"]
	if raw == "" {
		return map[string]any{}, nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("parse config.json: %w", err)
	}
	return data, nil
}

func (b *KubeBackend) ConfigGet(name, key string) (any, error) {
	ck, ok := agent.LookupConfigKey(key)
	if !ok {
		return nil, fmt.Errorf("unknown config key: %s", key)
	}

	// API keys and env-injected secrets are stored in Secret.
	if isSecretStoredKey(ck) {
		ctx, cancel := b.ctx()
	defer cancel()
		var secret Secret
		if err := b.client.Get(ctx, "secrets", b.resourceName(name), &secret); err != nil {
			return nil, err
		}
		return decodeSecretValue(secret.Data[key]), nil
	}

	// Everything else (including telegram_token, gateway_paired_tokens) is in ConfigMap.
	data, err := b.configData(name)
	if err != nil {
		return nil, err
	}
	return getNestedPath(data, ck.Path), nil
}

func (b *KubeBackend) ConfigSet(name, key, value string) error {
	ck, ok := agent.LookupConfigKey(key)
	if !ok {
		return fmt.Errorf("unknown config key: %s", key)
	}

	// API keys and env-injected secrets go to Secret.
	if isSecretStoredKey(ck) {
		return b.setSecretKey(name, key, value)
	}

	ctx, cancel := b.ctx()
	defer cancel()
	resName := b.resourceName(name)

	var cm ConfigMap
	if err := b.client.Get(ctx, "configmaps", resName, &cm); err != nil {
		return err
	}

	// Parse config.json from the already-fetched ConfigMap (single GET,
	// avoids lost-update race from a second independent GET).
	data := map[string]any{}
	if raw := cm.Data["config.json"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			return fmt.Errorf("parse config.json: %w", err)
		}
	}
	setNestedPath(data, ck.Path, value)

	cfgJSON, _ := json.Marshal(data)
	cm.Data["config.json"] = string(cfgJSON)

	return b.client.Update(ctx, "configmaps", resName, cm, nil)
}

func (b *KubeBackend) ConfigGetAll(name string) (map[string]any, error) {
	data, err := b.configData(name)
	if err != nil {
		return nil, err
	}

	// Read Secret for provider keys (stored separately from ConfigMap).
	ctx, cancel := b.ctx()
	defer cancel()
	var secret Secret
	hasSecret := false
	if err := b.client.Get(ctx, "secrets", b.resourceName(name), &secret); err != nil {
		if !IsNotFound(err) {
			return nil, fmt.Errorf("read secret for %s: %w", name, err)
		}
	} else {
		hasSecret = true
	}

	result := map[string]any{}
	for _, ck := range agent.ConfigKeys {
		if isSecretStoredKey(ck) && hasSecret {
			if v, ok := secret.Data[ck.Name]; ok && v != "" {
				if decoded := decodeSecretValue(v); decoded != "" {
					result[ck.Name] = decoded
					continue
				}
			}
			if v, ok := secret.StringData[ck.Name]; ok && v != "" {
				result[ck.Name] = v
				continue
			}
		}
		val := getNestedPath(data, ck.Path)
		if val != nil {
			result[ck.Name] = val
		}
	}
	return result, nil
}

func (b *KubeBackend) ConfigGetAllRedacted(name string) (map[string]any, error) {
	all, err := b.ConfigGetAll(name)
	if err != nil {
		return nil, err
	}

	for _, ck := range agent.ConfigKeys {
		if ck.Redacted {
			if v, ok := all[ck.Name]; ok {
				if s, ok := v.(string); ok && s != "" {
					all[ck.Name] = "***"
				}
			}
		}
	}
	return all, nil
}

// ---------------------------------------------------------------------------
// Metadata
// ---------------------------------------------------------------------------

func (b *KubeBackend) ReadMeta(name string) (map[string]string, error) {
	cm, err := b.configMap(name)
	if err != nil {
		return nil, err
	}

	result := map[string]string{}
	for k, v := range cm.Metadata.Annotations {
		if strings.HasPrefix(k, "xnc.io/meta-") {
			result[strings.TrimPrefix(k, "xnc.io/meta-")] = v
		}
	}
	return result, nil
}

func (b *KubeBackend) WriteMeta(name, key, value string) error {
	return b.WriteMetaBatch(name, map[string]string{key: value})
}

func (b *KubeBackend) WriteMetaBatch(name string, pairs map[string]string) error {
	ctx, cancel := b.ctx()
	defer cancel()
	resName := b.resourceName(name)

	var cm ConfigMap
	if err := b.client.Get(ctx, "configmaps", resName, &cm); err != nil {
		return err
	}

	if cm.Metadata.Annotations == nil {
		cm.Metadata.Annotations = map[string]string{}
	}
	for k, v := range pairs {
		if !validMetaKey.MatchString(k) {
			return fmt.Errorf("invalid metadata key %q", k)
		}
		if len(v) > maxMetaValueSize {
			return fmt.Errorf("metadata value for %q too large: %d bytes (max %d)", k, len(v), maxMetaValueSize)
		}
		cm.Metadata.Annotations["xnc.io/meta-"+k] = v
	}

	return b.client.Update(ctx, "configmaps", resName, cm, nil)
}

// ---------------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------------

func (b *KubeBackend) ReadToken(name string) (string, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	var secret Secret
	if err := b.client.Get(ctx, "secrets", b.resourceName(name), &secret); err != nil {
		return "", err
	}
	return decodeSecretValue(secret.Data["auth_token"]), nil
}

func (b *KubeBackend) SetupWebhookAuth(name string) (string, error) {
	// Generate token using the shared agent package function.
	token, err := agent.GenerateToken()
	if err != nil {
		return "", err
	}

	// Store raw token in Secret.
	if err := b.setSecretKey(name, "auth_token", token); err != nil {
		return "", err
	}

	// Store SHA-256 hash in ConfigMap config.json under gateway.paired_tokens.
	hash := agent.HashToken(token)

	ctx, cancel := b.ctx()
	defer cancel()
	resName := b.resourceName(name)

	var cm ConfigMap
	if err := b.client.Get(ctx, "configmaps", resName, &cm); err != nil {
		return "", err
	}

	// Parse config.json from the already-fetched ConfigMap.
	data := map[string]any{}
	if raw := cm.Data["config.json"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			return "", fmt.Errorf("parse config.json: %w", err)
		}
	}
	setNestedPath(data, "gateway.paired_tokens", []string{hash})
	setNestedPath(data, "gateway.require_pairing", true)

	cfgJSON, _ := json.Marshal(data)
	cm.Data["config.json"] = string(cfgJSON)

	if err := b.client.Update(ctx, "configmaps", resName, cm, nil); err != nil {
		return "", err
	}

	return token, nil
}

// ---------------------------------------------------------------------------
// Provider keys
// ---------------------------------------------------------------------------

func (b *KubeBackend) HasProviderKey(name string) bool {
	ctx, cancel := b.ctx()
	defer cancel()
	var secret Secret
	if err := b.client.Get(ctx, "secrets", b.resourceName(name), &secret); err != nil {
		return false
	}
	for _, ck := range agent.ConfigKeys {
		if ck.Provider != "" {
			if v, ok := secret.Data[ck.Name]; ok && v != "" {
				if decoded := decodeSecretValue(v); decoded != "" {
					return true
				}
			}
			// Also check stringData (set during creation, not base64-encoded).
			if v, ok := secret.StringData[ck.Name]; ok && v != "" {
				return true
			}
		}
	}
	return false
}

func (b *KubeBackend) CollectKeys() map[string]string {
	agents, err := b.ListAll()
	if err != nil {
		return map[string]string{}
	}
	result := map[string]string{}
	ctx, cancel := b.ctx()
	defer cancel()
	for _, a := range agents {
		var secret Secret
		if err := b.client.Get(ctx, "secrets", b.resourceName(a.Name), &secret); err != nil {
			continue
		}
		for _, ck := range agent.ConfigKeys {
			if ck.Provider == "" {
				continue
			}
			if v, ok := secret.Data[ck.Name]; ok && v != "" {
				decoded := decodeSecretValue(v)
				if decoded != "" {
					key := ck.Provider
					if _, exists := result[key]; !exists {
						result[key] = decoded
					}
				}
			}
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Container support
// ---------------------------------------------------------------------------

func (b *KubeBackend) ContainerEnv(name string) ([]string, error) {
	data, err := b.configData(name)
	if err != nil {
		return nil, err
	}

	// Read Secret for API keys (stored separately from ConfigMap).
	ctx, cancel := b.ctx()
	defer cancel()
	var secret Secret
	hasSecret := false
	if err := b.client.Get(ctx, "secrets", b.resourceName(name), &secret); err != nil {
		if !IsNotFound(err) {
			return nil, fmt.Errorf("read secret for %s: %w", name, err)
		}
	} else {
		hasSecret = true
	}

	// Gateway bind: nullclaw defaults to 127.0.0.1 which is unreachable
	// from outside the pod. Bind to all interfaces so Services can reach it.
	env := []string{
		"NULLCLAW_GATEWAY_HOST=0.0.0.0",
		"NULLCLAW_ALLOW_PUBLIC_BIND=true",
	}

	// Web channel auth token — read from Secret and pass as env var.
	if hasSecret {
		if v, ok := secret.Data["auth_token"]; ok && v != "" {
			if decoded := decodeSecretValue(v); decoded != "" {
				env = append(env, "NULLCLAW_WEB_TOKEN="+decoded)
			}
		} else if v, ok := secret.StringData["auth_token"]; ok && v != "" {
			env = append(env, "NULLCLAW_WEB_TOKEN="+v)
		}
	}
	for _, ck := range agent.ConfigKeys {
		if ck.EnvVar == "" {
			continue
		}
		// Redacted keys (API keys, tokens) are in the Secret, not the ConfigMap.
		if ck.Redacted && hasSecret {
			if v, ok := secret.Data[ck.Name]; ok && v != "" {
				decoded := decodeSecretValue(v)
				if decoded != "" {
					env = append(env, ck.EnvVar+"="+decoded)
					continue
				}
			}
			// Also check StringData (may be set during creation before API round-trip).
			if v, ok := secret.StringData[ck.Name]; ok && v != "" {
				env = append(env, ck.EnvVar+"="+v)
				continue
			}
		}
		// Non-secret config values from ConfigMap.
		val := getNestedPath(data, ck.Path)
		if s, ok := val.(string); ok && s != "" {
			env = append(env, ck.EnvVar+"="+s)
		}
	}
	return env, nil
}

func (b *KubeBackend) Dir(_ string) string {
	return "" // no filesystem directory in K8s mode
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (b *KubeBackend) setSecretKey(name, key, value string) error {
	ctx, cancel := b.ctx()
	defer cancel()
	resName := b.resourceName(name)

	var secret Secret
	if err := b.client.Get(ctx, "secrets", resName, &secret); err != nil {
		return err
	}

	if secret.StringData == nil {
		secret.StringData = map[string]string{}
	}
	secret.StringData[key] = value

	return b.client.Update(ctx, "secrets", resName, secret, nil)
}

// setNestedPath sets a value at a dot-separated JSON path.
func setNestedPath(data map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	current := data
	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
			return
		}
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
}

// getNestedPath reads a value at a dot-separated JSON path.
func getNestedPath(data map[string]any, path string) any {
	parts := strings.Split(path, ".")
	var current any = data
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[part]
	}
	return current
}
