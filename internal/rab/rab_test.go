package rab

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vibede/internal/diff"
	"vibede/internal/watch"
)

func TestSaveIsGzipAndReloads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	a := Open(path, 10, 10)
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	a.AddChange(watch.Change{
		Path: "main.go", Kind: "modified", At: at,
		Stats: diff.Stats{Added: 2, Removed: 1},
		Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: "+", NewNo: 4, Text: "x := 1"}}}},
	})
	a.AddExplanation(Explanation{
		At: at, Path: "main.go", Question: "why?", Answer: "because", Model: "tiny",
		Context: []string{"main.go"},
	})
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		t.Fatalf("archive is not a gzip stream: %x", raw[:2])
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	decoded := new(bytes.Buffer)
	if _, err := decoded.ReadFrom(zr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(decoded.String(), "\n\t\"version\"") {
		t.Fatalf("payload looks uncompressed: %q", decoded.String()[:60])
	}
	if !strings.Contains(decoded.String(), "because") {
		t.Fatalf("payload missing explanation: %s", decoded.String())
	}

	reopened := Open(path, 10, 10)
	doc := reopened.Snapshot()
	if len(doc.Changes) != 1 || doc.Changes[0].Path != "main.go" || doc.Changes[0].Stats.Added != 2 {
		t.Fatalf("changes = %+v", doc.Changes)
	}
	if len(doc.Explanations) != 1 || doc.Explanations[0].Answer != "because" {
		t.Fatalf("explanations = %+v", doc.Explanations)
	}
	if _, _, size := reopened.Stats(); size != int64(len(raw)) {
		t.Fatalf("stats size = %d, want %d", size, len(raw))
	}
}

func TestArchiveKeepsNewestEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	a := Open(path, 2, 1)
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		a.AddChange(watch.Change{Path: string(rune('a' + i)), At: base.Add(time.Duration(i) * time.Second)})
		a.AddExplanation(Explanation{Question: string(rune('A' + i)), Answer: "x", At: base})
	}
	doc := a.Snapshot()
	if len(doc.Changes) != 2 || doc.Changes[0].Path != "c" || doc.Changes[1].Path != "b" {
		t.Fatalf("changes = %+v", doc.Changes)
	}
	if len(doc.Explanations) != 1 || doc.Explanations[0].Question != "C" {
		t.Fatalf("explanations = %+v", doc.Explanations)
	}
}

func TestCorruptArchiveStartsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := Open(path, 5, 5)
	if doc := a.Snapshot(); len(doc.Changes) != 0 || len(doc.Explanations) != 0 {
		t.Fatalf("corrupt archive loaded entries: %+v", doc)
	}
	a.AddExplanation(Explanation{Question: "q", Answer: "a"})
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if got := Open(path, 5, 5).Snapshot(); len(got.Explanations) != 1 {
		t.Fatalf("replacement archive = %+v", got)
	}
}
