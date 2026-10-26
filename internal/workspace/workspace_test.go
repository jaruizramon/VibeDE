package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"vibede/internal/config"
)

func testWorkspace(t *testing.T) (*Workspace, string) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, data string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main\n")
	write("dir/a.py", "print('a')\n")
	write("dir/b.txt", "text\n")
	write("node_modules/pkg/index.js", "module.exports = 1\n")
	write("bin.dat", "a\x00b")
	cfg := config.Config{Root: root, Ignore: config.DefaultIgnore, Limits: config.DefaultLimits()}
	return New(cfg), root
}

func TestResolveRejectsTraversalAndSymlinkEscape(t *testing.T) {
	ws, root := testWorkspace(t)
	for _, bad := range []string{"../secret", "dir/../../secret", "/etc/passwd"} {
		if _, err := ws.Resolve(bad); !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("Resolve(%q) error = %v, want ErrOutsideRoot", bad, err)
		}
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Resolve("escape"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("symlink escape error = %v", err)
	}
	if _, err := ws.Resolve("dir/a.py"); err != nil {
		t.Fatalf("valid path: %v", err)
	}
}

func TestListHidesIgnoredAndSortsDirectoriesFirst(t *testing.T) {
	ws, _ := testWorkspace(t)
	entries, err := ws.List("")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
		if e.Name == "node_modules" {
			t.Fatal("node_modules should be ignored")
		}
	}
	if len(names) < 2 || names[0] != "dir" {
		t.Fatalf("entries = %v, want dir first", names)
	}
	if _, err := ws.List("node_modules"); err != nil {
		t.Fatalf("listing an ignored dir should be empty, got %v", err)
	}
}

func TestReadRejectsBinaryAndDirectories(t *testing.T) {
	ws, _ := testWorkspace(t)
	if _, err := ws.Read("bin.dat"); !errors.Is(err, ErrBinary) {
		t.Fatalf("binary error = %v", err)
	}
	if _, err := ws.Read(""); err == nil {
		t.Fatal("root directory should not be readable as a file")
	}
	if _, err := ws.Read("missing.go"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing error = %v", err)
	}
	b, err := ws.Read("dir/a.py")
	if err != nil || string(b) != "print('a')\n" {
		t.Fatalf("read = %q, %v", b, err)
	}
}

func TestLanguageMapping(t *testing.T) {
	cases := map[string]string{
		"a.go": "go", "a.py": "python", "a.tsx": "typescriptreact",
		"a.jsx": "javascriptreact", "a.css": "css", "a.hpp": "cpp",
		"a.rs": "rust", "a.html": "html", "a.unknown": "",
	}
	for name, want := range cases {
		if got := Language(name); got != want {
			t.Errorf("Language(%q) = %q, want %q", name, got, want)
		}
	}
	if ServerGroup("typescriptreact") != "typescript" || ServerGroup("hpp") != "" {
		t.Fatal("unexpected server groups")
	}
}
