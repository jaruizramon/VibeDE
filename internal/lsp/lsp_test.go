package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vibede/internal/config"
	"vibede/internal/workspace"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGoplsNavigationAndDiagnostics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping language server integration in -short mode")
	}
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod": "module example.com/demo\n\ngo 1.23\n",
		"main.go": `package main

func helper() int { return 42 }

func main() {
	x := helper()
	_ = x
}
`,
	})
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.New(cfg)
	m := NewManager(ws, cfg, nil)
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := m.Open(ctx, "main.go"); err != nil {
		t.Fatalf("open: %v", err)
	}
	locs, err := m.Navigate(ctx, "main.go", 5, 7, "definition")
	if err != nil {
		t.Fatalf("definition: %v", err)
	}
	if len(locs) == 0 || locs[0].Path != "main.go" || locs[0].StartLine != 2 {
		t.Fatalf("definition = %+v, want main.go:3", locs)
	}
	refs, err := m.Navigate(ctx, "main.go", 2, 6, "references")
	if err != nil {
		t.Fatalf("references: %v", err)
	}
	if len(refs) < 2 {
		t.Fatalf("references = %+v, want declaration and call", refs)
	}
	syms, err := m.Symbols(ctx, "main.go")
	if err != nil {
		t.Fatalf("symbols: %v", err)
	}
	var names []string
	for _, s := range syms {
		names = append(names, s.Name)
	}
	if len(names) < 2 {
		t.Fatalf("symbols = %v", names)
	}
	// Diagnostics arrive asynchronously; this file is clean, but the manager
	// must answer a query without blocking.
	_ = m.DiagnosticsFor("main.go")
}
