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
