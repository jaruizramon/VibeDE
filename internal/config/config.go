// Package config loads VibeDE's workspace settings.
//
// VibeDE never writes configuration files. Optional .vibede.json in the
// workspace root is read once at startup; command line flags win.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ServerOverride customises how a language server is launched. A key is a
// language server group: go, python, typescript, html, css, json, c, rust.
type ServerOverride struct {
	Command  string   `json:"command,omitempty"`
	Args     []string `json:"args,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
}

type fileConfig struct {
	Ignore    []string                  `json:"ignore,omitempty"`
	Servers   map[string]ServerOverride `json:"servers,omitempty"`
	Limits    Limits                    `json:"limits,omitempty"`
	Watch     WatchConfig               `json:"watch,omitempty"`
	AI        AIConfig                  `json:"ai,omitempty"`
	NoServers bool                      `json:"noServers,omitempty"`
}

// AIConfig points at an OpenAI-compatible chat endpoint used to explain a
// selected code range. Something small and local is the intended target:
// llama.cpp, vLLM and friends all serve /v1/chat/completions.
type AIConfig struct {
	// Enabled is resolved (file/env/flags) and false when Disabled is set.
	Enabled   bool   `json:"enabled,omitempty"`
	Disabled  bool   `json:"disabled,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"` // base URL ending in /v1
	Model     string `json:"model,omitempty"`    // empty = first model the endpoint serves
	APIKey    string `json:"apiKey,omitempty"`
	MaxTokens int    `json:"maxTokens,omitempty"`
	TimeoutMS int    `json:"timeoutMs,omitempty"`
	Thinking  bool   `json:"thinking,omitempty"` // let reasoning models think before answering
}

// defaultAIEndpoint is llama.cpp's conventional OpenAI-compatible address.
// VibeDE is only a client here; it never binds this port.
const defaultAIEndpoint = "http://127.0.0.1:8080/v1"

// DefaultAI is the local-first explanation endpoint. Point Endpoint elsewhere
// (or set ai.disabled) to opt out.
func DefaultAI() AIConfig {
	return AIConfig{
		Enabled:   true,
		Endpoint:  defaultAIEndpoint,
		MaxTokens: 1200,
		TimeoutMS: 120000,
	}
}

// Limits bounds how much file data VibeDE keeps in memory.
type Limits struct {
	MaxFileBytes      int64 `json:"maxFileBytes,omitempty"`
	MaxSnapshotBytes  int64 `json:"maxSnapshotBytes,omitempty"`
	SnapshotBudget    int64 `json:"snapshotBudgetBytes,omitempty"`
	MaxDiagramNodes   int   `json:"maxDiagramNodes,omitempty"`
	MaxRenderedLines  int   `json:"maxRenderedLines,omitempty"`
	DiffComplexityCap int   `json:"diffComplexityCap,omitempty"`
}

// WatchConfig controls the filesystem watcher.
type WatchConfig struct {
	DebounceMS int `json:"debounceMs,omitempty"`
	MaxEvents  int `json:"maxEvents,omitempty"`
}

// Config is the resolved runtime configuration.
type Config struct {
	Root      string
	Ignore    []string
	Servers   map[string]ServerOverride
	Limits    Limits
	Watch     WatchConfig
	AI        AIConfig
	NoServers bool
	Port      int
	Open      bool
}

// DefaultIgnore contains directories that are noise for code tracing. The
// entries are matched against every path segment, so a nested node_modules is
// ignored as well.
var DefaultIgnore = []string{
	".git", ".hg", ".svn",
	"node_modules", "__pycache__", ".venv", "venv", ".mypy_cache",
	".pytest_cache", ".ruff_cache", ".tox", ".cache", ".next", ".nuxt",
	".svelte-kit", "dist", "build", "target", ".idea", ".vscode",
	// VibeDE's own history archive: written by the server, never part of the
	// workspace view or the change feed.
	".vibede.rab", ".vibede.rab.tmp",
}

// DefaultLimits are chosen so a normal repository is fully tracked without
// pinning hundreds of megabytes. Larger files are still viewable, but their
// pre-edit snapshot is not retained.
func DefaultLimits() Limits {
	return Limits{
		MaxFileBytes:      8 << 20,
		MaxSnapshotBytes:  1 << 20,
		SnapshotBudget:    256 << 20,
		MaxDiagramNodes:   400,
		MaxRenderedLines:  12000,
		DiffComplexityCap: 3000,
	}
}

// Load resolves configuration for root. .vibede.json is optional.
func Load(root string) (Config, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Config{}, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return Config{}, fmt.Errorf("workspace %s: %w", root, err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return Config{}, fmt.Errorf("workspace %s: %w", root, err)
	}
	if !st.IsDir() {
		return Config{}, fmt.Errorf("workspace %s is not a directory", abs)
	}
	cfg := Config{
		Root:    abs,
		Ignore:  append([]string(nil), DefaultIgnore...),
		Servers: map[string]ServerOverride{},
		Limits:  DefaultLimits(),
		AI:      DefaultAI(),
		Watch: WatchConfig{
			DebounceMS: 120,
			MaxEvents:  500,
		},
	}
	raw, err := os.ReadFile(filepath.Join(abs, ".vibede.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf(".vibede.json: %w", err)
	}
	if err == nil {
		var fc fileConfig
		if err := json.Unmarshal(raw, &fc); err != nil {
			return Config{}, fmt.Errorf(".vibede.json: %w", err)
		}
		cfg.Ignore = append(cfg.Ignore, fc.Ignore...)
		cfg.NoServers = fc.NoServers
		for k, v := range fc.Servers {
			cfg.Servers[k] = v
		}
		mergeLimits(&cfg.Limits, fc.Limits)
		mergeAI(&cfg.AI, &fc.AI)
		if fc.Watch.DebounceMS > 0 {
			cfg.Watch.DebounceMS = fc.Watch.DebounceMS
		}
		if fc.Watch.MaxEvents > 0 {
			cfg.Watch.MaxEvents = fc.Watch.MaxEvents
		}
	}
	// Environment overrides the file, flags override both.
	mergeAI(&cfg.AI, &AIConfig{
		Endpoint: os.Getenv("VIBEDE_AI_ENDPOINT"),
		Model:    os.Getenv("VIBEDE_AI_MODEL"),
		APIKey:   os.Getenv("VIBEDE_AI_KEY"),
	})
	return cfg, nil
}

// mergeAI applies non-zero fields of src to dst. Disabled wins.
func mergeAI(dst *AIConfig, src *AIConfig) {
	if src.Enabled {
		dst.Enabled = true
	}
	if src.Disabled {
		dst.Enabled = false
	}
	if src.Endpoint != "" {
		dst.Endpoint = src.Endpoint
	}
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.APIKey != "" {
		dst.APIKey = src.APIKey
	}
	if src.MaxTokens > 0 {
		dst.MaxTokens = src.MaxTokens
	}
	if src.TimeoutMS > 0 {
		dst.TimeoutMS = src.TimeoutMS
	}
	if src.Thinking {
		dst.Thinking = true
	}
}

// ApplyAIOverrides merges command-line flag values into cfg. A nil src means
// no flag changed anything.
func ApplyAIOverrides(cfg *Config, src *AIConfig) {
	if src == nil {
		return
	}
	mergeAI(&cfg.AI, src)
}

func mergeLimits(dst *Limits, src Limits) {
	if src.MaxFileBytes > 0 {
		dst.MaxFileBytes = src.MaxFileBytes
	}
	if src.MaxSnapshotBytes > 0 {
		dst.MaxSnapshotBytes = src.MaxSnapshotBytes
	}
	if src.SnapshotBudget > 0 {
		dst.SnapshotBudget = src.SnapshotBudget
	}
	if src.MaxDiagramNodes > 0 {
		dst.MaxDiagramNodes = src.MaxDiagramNodes
	}
	if src.MaxRenderedLines > 0 {
		dst.MaxRenderedLines = src.MaxRenderedLines
	}
	if src.DiffComplexityCap > 0 {
		dst.DiffComplexityCap = src.DiffComplexityCap
	}
}

// Debounce is the watcher debounce duration.
func (c Config) Debounce() time.Duration {
	return time.Duration(c.Watch.DebounceMS) * time.Millisecond
}
