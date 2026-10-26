package apphost

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func writeRoot(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name+".go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func infoRoot(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url + "api/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var info struct {
		Root string `json:"root"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		t.Fatalf("info: %v: %s", err, body)
	}
	return info.Root
}

func TestStartSwitchAndInvalidRoot(t *testing.T) {
	root1 := writeRoot(t, "one")
	root2 := writeRoot(t, "two")
	m := New(Options{ListenHost: "127.0.0.1", Port: 0, NoServers: true})
	defer m.Close()

	url1, err := m.Start(root1)
	if err != nil {
		t.Fatal(err)
	}
	if got := infoRoot(t, url1); got != root1 {
		t.Fatalf("root1 served %q, want %q", got, root1)
	}

	url2, err := m.Switch(root2)
	if err != nil {
		t.Fatal(err)
	}
	if got := infoRoot(t, url2); got != root2 {
		t.Fatalf("root2 served %q, want %q", got, root2)
	}
	if m.Root() != root2 {
		t.Fatalf("Root() = %q", m.Root())
	}

	if _, err := m.Switch(filepath.Join(root2, "missing")); err == nil {
		t.Fatal("switching to a missing root should fail")
	}
	if m.Root() != root2 {
		t.Fatalf("failed switch changed root to %q", m.Root())
	}
	if got := infoRoot(t, url2); got != root2 {
		t.Fatalf("existing server broken after failed switch: %q", got)
	}
}
