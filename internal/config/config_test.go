package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaultsAndOverrides(t *testing.T) {
	root := t.TempDir()
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limits.MaxFileBytes != DefaultLimits().MaxFileBytes {
		t.Fatalf("defaults = %+v", cfg.Limits)
	}
	if cfg.Debounce() != 120*time.Millisecond {
		t.Fatalf("debounce = %v", cfg.Debounce())
	}
	body := `{
		"ignore": ["generated"],
		"limits": {"maxFileBytes": 1234, "maxRenderedLines": 99},
		"watch": {"debounceMs": 40},
		"servers": {
			"python": {"command": "my-pylsp", "args": ["--stdio"]},
			"go": {"disabled": true}
		}
	}`
	if err := os.WriteFile(filepath.Join(root, ".vibede.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limits.MaxFileBytes != 1234 || cfg.Limits.MaxRenderedLines != 99 {
		t.Fatalf("limits = %+v", cfg.Limits)
	}
	if cfg.Debounce() != 40*time.Millisecond {
		t.Fatalf("debounce = %v", cfg.Debounce())
	}
	found := false
	for _, p := range cfg.Ignore {
		if p == "generated" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ignore = %v", cfg.Ignore)
	}
	if cfg.Servers["python"].Command != "my-pylsp" || !cfg.Servers["go"].Disabled {
		t.Fatalf("servers = %+v", cfg.Servers)
	}
}

func TestLoadAIConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("VIBEDE_AI_ENDPOINT", "")
	t.Setenv("VIBEDE_AI_MODEL", "")
	t.Setenv("VIBEDE_AI_KEY", "")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AI.Enabled || cfg.AI.Endpoint != defaultAIEndpoint || cfg.AI.MaxTokens != 1200 {
		t.Fatalf("defaults = %+v", cfg.AI)
	}
	body := `{"ai": {"endpoint": "http://127.0.0.1:9000/v1", "model": "tiny", "apiKey": "k", "maxTokens": 300, "timeoutMs": 5000, "thinking": true}}`
	if err := os.WriteFile(filepath.Join(root, ".vibede.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Endpoint != "http://127.0.0.1:9000/v1" || cfg.AI.Model != "tiny" ||
		cfg.AI.APIKey != "k" || cfg.AI.MaxTokens != 300 || cfg.AI.TimeoutMS != 5000 || !cfg.AI.Thinking {
		t.Fatalf("file config = %+v", cfg.AI)
	}

	// Environment beats the file; flags beat both.
	t.Setenv("VIBEDE_AI_MODEL", "from-env")
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Model != "from-env" {
		t.Fatalf("env model = %q", cfg.AI.Model)
	}
	ApplyAIOverrides(&cfg, &AIConfig{Model: "from-flag", Disabled: true})
	if cfg.AI.Model != "from-flag" || cfg.AI.Enabled {
		t.Fatalf("flag override = %+v", cfg.AI)
	}
}

func TestLoadInvalidConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".vibede.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("invalid config should fail")
	}
}

func TestLoadRejectsFileRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file); err == nil {
		t.Fatal("file root should fail")
	}
}
