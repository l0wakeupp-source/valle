package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"rick/internal/provider/catalog"
)

// Credentials is the on-disk auth store: ~/.config/rick/auth.json.
//
// It is kept separate from rick.json so a project config can be committed to
// version control without leaking keys, and so the file can be chmod 0600.
type Credentials struct {
	Providers map[string]Credential `json:"provider"`
	// rotationIndex tracks round-robin/failover key position per provider.
	// Not persisted — reset on each session.
	rotationIndex map[string]int `json:"-"`
}

// Credential is one saved provider login.
type Credential struct {
	Type    string   `json:"type,omitempty"` // anthropic | openai (wire flavor)
	APIKey  string   `json:"apiKey,omitempty"`
	BaseURL string   `json:"baseUrl,omitempty"`
	Label   string   `json:"label,omitempty"` // display name
	Models  []string `json:"models,omitempty"`
	// ContextWindows maps model id -> window size, as reported by the
	// endpoint or inferred at connect time.
	ContextWindows  map[string]int `json:"context_windows,omitempty"` // last fetched model ids
	VisionModels    []string       `json:"vision_models,omitempty"`
	ModalitiesKnown bool           `json:"modalities_known,omitempty"`
	Default         string         `json:"default,omitempty"` // preferred model id
	Custom          bool           `json:"custom,omitempty"`  // user-added, not in the catalog
	Disabled        bool           `json:"disabled,omitempty"`
	// OnlyFree filters model listings to zero-cost / :free models only.
	OnlyFree bool `json:"only_free,omitempty"`
	// APIKeys holds multiple API keys for key rotation. When APIKey is set
	// and this is empty, it's treated as a single-key config.
	APIKeys []string `json:"apiKeys,omitempty"`
	// APIKeyMode controls how multiple keys are used:
	// "single" (default) - use the first key only
	// "round-robin" - rotate through keys on each request
	// "failover" - rotate to next key on rate-limit/quota errors
	APIKeyMode string `json:"apiKeyMode,omitempty"` // single | round-robin | failover
}

// AuthPath is the credential file location.
func AuthPath() string { return filepath.Join(GlobalDir(), "auth.json") }

// LoadCredentials reads the auth store, returning an empty one if absent.
func LoadCredentials() (*Credentials, error) {
	c := &Credentials{Providers: map[string]Credential{}}
	data, err := os.ReadFile(AuthPath())
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(StripJSONC(data), c); err != nil {
		return c, fmt.Errorf("%s: %w", AuthPath(), err)
	}
	if c.Providers == nil {
		c.Providers = map[string]Credential{}
	}
	for id, p := range c.Providers {
		p.APIKey = catalog.CleanSecret(p.APIKey)
		p.BaseURL = catalog.CleanSecret(p.BaseURL)
		c.Providers[id] = p
	}
	return c, nil
}

// Save atomically writes the auth store with owner-only permissions.
func (c *Credentials) Save() error {
	if c.Providers == nil {
		c.Providers = map[string]Credential{}
	}
	dir := GlobalDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	final := AuthPath()
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	_ = os.Chmod(final, 0o600)
	return nil
}

// Set upserts a credential.
func (c *Credentials) Set(id string, cred Credential) {
	if c.Providers == nil {
		c.Providers = map[string]Credential{}
	}
	c.Providers[id] = cred
}

// Remove deletes a credential.
func (c *Credentials) Remove(id string) { delete(c.Providers, id) }

// AllKeys returns the effective list of API keys for a credential.
// When APIKey is set and APIKeys is empty, it returns [APIKey].
func (c *Credentials) AllKeys(id string) []string {
	cred, ok := c.Providers[id]
	if !ok {
		return nil
	}
	if len(cred.APIKeys) > 0 {
		return cred.APIKeys
	}
	if cred.APIKey != "" {
		return []string{cred.APIKey}
	}
	return nil
}

// CurrentKey returns the active key based on mode and rotation state.
func (c *Credentials) CurrentKey(id string) string {
	keys := c.AllKeys(id)
	if len(keys) == 0 {
		return ""
	}
	mode := c.Providers[id].APIKeyMode
	if mode == "" {
		mode = "single"
	}
	if mode == "round-robin" || mode == "failover" {
		// Use the key index based on rotation counter.
		idx := c.rotationIndex[id] % len(keys)
		return keys[idx]
	}
	return keys[0]
}

// RotateKey advances the rotation counter and returns the next key.
func (c *Credentials) RotateKey(id string) string {
	if c.rotationIndex == nil {
		c.rotationIndex = map[string]int{}
	}
	c.rotationIndex[id]++
	return c.CurrentKey(id)
}

// rotationIndex tracks round-robin/failover key position per provider.
func (c *Credentials) ensureRotation() {
	if c.rotationIndex == nil {
		c.rotationIndex = map[string]int{}
	}
}


// IDs lists configured provider ids, sorted.
func (c *Credentials) IDs() []string {
	out := make([]string, 0, len(c.Providers))
	for id := range c.Providers {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// MergeCredentials overlays the auth store onto a loaded config. Credentials
// never override an explicit provider block in rick.json — a project that
// pins its own endpoint keeps it.
func MergeCredentials(cfg *Config, creds *Credentials) {
	if creds == nil || len(creds.Providers) == 0 {
		return
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]Provider{}
	}
	for id, cred := range creds.Providers {
		if cred.Disabled {
			continue
		}
		p := cfg.Providers[id]
		if p.Type == "" {
			p.Type = cred.Type
		}
		if p.APIKey == "" {
			p.APIKey = cred.APIKey
		}
		if p.BaseURL == "" {
			p.BaseURL = cred.BaseURL
		}
		cfg.Providers[id] = p
	}
}

// FirstConfiguredModel picks a sensible default model after login: the
// credential's preferred model, else its first fetched model.
func FirstConfiguredModel(creds *Credentials, id string) string {
	cred, ok := creds.Providers[id]
	if !ok {
		return ""
	}
	if cred.Default != "" {
		return id + "/" + cred.Default
	}
	if len(cred.Models) > 0 {
		return id + "/" + cred.Models[0]
	}
	return ""
}
