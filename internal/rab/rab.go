// Package rab stores VibeDE's recent-edit deltas and AI explanations in one
// self-contained, gzip-compressed archive: the .rab file.
//
// The archive is a single JSON document compressed with gzip. It contains the
// change deltas VibeDE observed and the explanations the model produced.
// Explanation context (code sent to the model) is never stored; only the
// question, answer, model name and the names of the files used.
package rab

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"vibede/internal/watch"
)

// FileName is the archive's fixed name inside the workspace root.
const FileName = ".vibede.rab"

const (
	version         = 1
	maxDecodedBytes = 64 << 20
	flushInterval   = time.Second
)

// Explanation is one AI answer about a code selection.
type Explanation struct {
	At        time.Time `json:"at"`
	Path      string    `json:"path,omitempty"`
	Language  string    `json:"language,omitempty"`
	Function  string    `json:"function,omitempty"`
	StartLine int       `json:"startLine,omitempty"`
	EndLine   int       `json:"endLine,omitempty"`
	Question  string    `json:"question"`
	Answer    string    `json:"answer"`
	Model     string    `json:"model,omitempty"`
	// Context lists file names that were sent with the question. File
	// contents are deliberately absent: the model context is cleared after
	// each answer.
	Context []string `json:"context,omitempty"`
	// Annotations are the answer parsed into line-anchored notes. They let
	// the editor render the explanation above the lines it describes.
	Annotations []Annotation `json:"annotations,omitempty"`
}

// Annotation is one line-anchored note parsed from a model answer.
type Annotation struct {
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine,omitempty"`
	Text      string `json:"text"`
}

// Document is the archive's payload.
type Document struct {
	Version      int            `json:"version"`
	Updated      time.Time      `json:"updated"`
	Changes      []watch.Change `json:"changes"`
	Explanations []Explanation  `json:"explanations"`
}

// Archive is a debounced, atomic .rab writer.
type Archive struct {
	mu              sync.Mutex
	path            string
	maxChanges      int
	maxExplanations int
	doc             Document
	dirty           bool
	bytes           int64
}

// Open loads an archive at path. A missing or corrupt file yields an empty
// archive; the next save replaces it.
func Open(path string, maxChanges, maxExplanations int) *Archive {
	if maxChanges < 0 {
		maxChanges = 0
	}
	if maxExplanations < 0 {
		maxExplanations = 0
	}
	a := &Archive{
		path:            path,
		maxChanges:      maxChanges,
		maxExplanations: maxExplanations,
		doc:             Document{Version: version},
	}
	a.load()
	return a
}

func (a *Archive) load() {
	f, err := os.Open(a.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("rab: open %s: %v", a.path, err)
		}
		return
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		log.Printf("rab: %s is not a gzip archive: %v", a.path, err)
		return
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, maxDecodedBytes+1))
	if err != nil {
		log.Printf("rab: read %s: %v", a.path, err)
		return
	}
	if len(raw) > maxDecodedBytes {
		log.Printf("rab: %s exceeds the %d byte decode limit; starting fresh", a.path, maxDecodedBytes)
		return
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		log.Printf("rab: %s: %v", a.path, err)
		return
	}
	doc.Version = version
	doc.Changes = trimHead(doc.Changes, a.maxChanges)
	doc.Explanations = trimHead(doc.Explanations, a.maxExplanations)
	a.doc = doc
	if st, err := f.Stat(); err == nil {
		a.bytes = st.Size()
	}
}

func trimHead[T any](in []T, max int) []T {
	if max <= 0 || len(in) <= max {
		return in
	}
	return in[:max]
}

// AddChange records a filesystem delta, newest first.
func (a *Archive) AddChange(ch watch.Change) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.Changes = append([]watch.Change{ch}, a.doc.Changes...)
	a.doc.Changes = trimHead(a.doc.Changes, a.maxChanges)
	a.dirty = true
}

// AddExplanation records an AI answer, newest first.
func (a *Archive) AddExplanation(ex Explanation) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.Explanations = append([]Explanation{ex}, a.doc.Explanations...)
	a.doc.Explanations = trimHead(a.doc.Explanations, a.maxExplanations)
	a.dirty = true
}

// Snapshot returns a shallow copy safe to encode while writers continue.
func (a *Archive) Snapshot() Document {
	a.mu.Lock()
	defer a.mu.Unlock()
	doc := a.doc
	doc.Changes = append([]watch.Change(nil), a.doc.Changes...)
	doc.Explanations = append([]Explanation(nil), a.doc.Explanations...)
	return doc
}

// Stats reports entry counts and the last known file size.
func (a *Archive) Stats() (changes, explanations int, bytes int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.doc.Changes), len(a.doc.Explanations), a.bytes
}

// Run flushes dirty archives at most once per second until ctx is done.
func (a *Archive) Run(ctx context.Context) {
	t := time.NewTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.flush()
		}
	}
}

func (a *Archive) flush() {
	a.mu.Lock()
	if !a.dirty {
		a.mu.Unlock()
		return
	}
	a.dirty = false
	a.mu.Unlock()
	if err := a.Save(); err != nil {
		log.Printf("rab: save %s: %v", a.path, err)
	}
}

// Save writes the archive atomically. The file is a gzip stream containing the
// JSON document, so the history is compressed inside the .rab file itself.
func (a *Archive) Save() error {
	a.mu.Lock()
	doc := a.doc
	doc.Version = version
	doc.Updated = time.Now().UTC()
	doc.Changes = append([]watch.Change(nil), a.doc.Changes...)
	doc.Explanations = append([]Explanation(nil), a.doc.Explanations...)
	if doc.Changes == nil {
		doc.Changes = []watch.Change{}
	}
	if doc.Explanations == nil {
		doc.Explanations = []Explanation{}
	}
	a.mu.Unlock()

	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(a.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := a.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	zw.Name = filepath.Base(a.path)
	zw.Comment = "VibeDE history: deltas and explanations"
	if _, err := zw.Write(raw); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, a.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if st, err := os.Stat(a.path); err == nil {
		a.mu.Lock()
		a.bytes = st.Size()
		a.mu.Unlock()
	}
	return nil
}

// Close flushes pending changes.
func (a *Archive) Close() error {
	a.mu.Lock()
	dirty := a.dirty
	a.mu.Unlock()
	if !dirty {
		return nil
	}
	return a.Save()
}
