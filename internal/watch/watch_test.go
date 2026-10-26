package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vibede/internal/config"
	"vibede/internal/workspace"
)

func newTest(t *testing.T) (*workspace.Workspace, string) {
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
	write("a.txt", "one\ntwo\n")
	cfg := config.Config{Root: root, Ignore: config.DefaultIgnore, Limits: config.DefaultLimits()}
	return workspace.New(cfg), root
}

func TestTrackerDiffsAndDeletion(t *testing.T) {
	ws, root := newTest(t)
	tr := NewTracker(ws, 1<<20, 1<<20, 1000)
	tr.Observe("a.txt", []byte("one\ntwo\n"))
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\nTWO\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, ok := tr.Apply("a.txt")
	if !ok || ch.Kind != "modified" {
		t.Fatalf("change = %+v ok=%v", ch, ok)
	}
	if ch.Stats.Added != 2 || ch.Stats.Removed != 1 {
		t.Fatalf("stats = %+v", ch.Stats)
	}
	if _, ok := tr.Apply("a.txt"); ok {
		t.Fatal("unchanged file should not emit another change")
	}
	if err := os.Remove(filepath.Join(root, "a.txt")); err != nil {
		t.Fatal(err)
	}
	ch, ok = tr.Apply("a.txt")
	if !ok || ch.Kind != "deleted" || ch.Stats.Removed != 3 {
		t.Fatalf("deleted change = %+v ok=%v", ch, ok)
	}
}

func TestTrackerCreatedFile(t *testing.T) {
	ws, root := newTest(t)
	tr := NewTracker(ws, 1<<20, 1<<20, 1000)
	if err := os.WriteFile(filepath.Join(root, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, ok := tr.Apply("new.go")
	if !ok || ch.Kind != "created" || ch.Stats.Added != 1 {
		t.Fatalf("created change = %+v ok=%v", ch, ok)
	}
}

func TestSeedThenApplyUnchanged(t *testing.T) {
	ws, _ := newTest(t)
	tr := NewTracker(ws, 1<<20, 1<<20, 1000)
	tr.Seed(context.Background())
	if !tr.Seeded() {
		t.Fatal("tracker not marked seeded")
	}
	if _, ok := tr.Apply("a.txt"); ok {
		t.Fatal("seeded unchanged file emitted a change")
	}
	files, bytes := tr.Stats()
	if files == 0 || bytes == 0 {
		t.Fatalf("stats = %d files, %d bytes", files, bytes)
	}
}

func TestWatcherReportsEditsAndIgnoresExcludedDirs(t *testing.T) {
	ws, root := newTest(t)
	tr := NewTracker(ws, 1<<20, 1<<20, 1000)
	tr.Seed(context.Background())
	events := make(chan Change, 16)
	w, err := New(ws, tr, 40*time.Millisecond, func(ch Change) { events <- ch })
	if err != nil {
		t.Fatal(err)
	}
	<-w.Ready()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	defer w.Close()

	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\nTWO\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ch := <-events:
			if ch.Path == "a.txt" && ch.Kind == "modified" {
				if ch.Stats.Added == 0 && ch.Stats.Removed == 0 {
					t.Fatalf("event has no diff: %+v", ch)
				}
				goto ignored
			}
		case <-deadline:
			t.Fatal("timed out waiting for a.txt change")
		}
	}
ignored:
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "x", "junk.js"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case ch := <-events:
		if ch.Path == "node_modules/x/junk.js" || ch.Path == "node_modules" {
			t.Fatalf("ignored path emitted %+v", ch)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

func TestTrackerRejectsBinarySnapshot(t *testing.T) {
	ws, root := newTest(t)
	tr := NewTracker(ws, 1<<20, 1<<20, 1000)
	tr.Observe("bin.dat", []byte{0, 1, 2})
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	ch, ok := tr.Apply("bin.dat")
	if !ok || !ch.Binary {
		t.Fatalf("binary change = %+v ok=%v", ch, ok)
	}
}
