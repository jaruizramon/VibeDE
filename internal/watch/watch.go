// Package watch turns filesystem activity into decoded change events with
// line diffs. It is the source of the live "agent edits" feed.
package watch

import (
	"context"
	"crypto/sha256"
	"errors"
	"log"
	"os"
	pathpkg "path"
	"sort"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"vibede/internal/diff"
	"vibede/internal/workspace"
)

// Change is one observed filesystem change.
type Change struct {
	Path         string      `json:"path"`
	Kind         string      `json:"kind"` // created, modified, deleted, renamed, directory
	IsDir        bool        `json:"isDir,omitempty"`
	Language     string      `json:"language,omitempty"`
	Size         int64       `json:"size,omitempty"`
	Binary       bool        `json:"binary,omitempty"`
	At           time.Time   `json:"at"`
	Stats        diff.Stats  `json:"stats"`
	Hunks        []diff.Hunk `json:"hunks,omitempty"`
	HunksOmitted bool        `json:"hunksOmitted,omitempty"`
}

// Tracker keeps the last-seen content of text files so a later write can be
// rendered as a diff. Snapshots are bounded in both per-file size and total
// memory; files beyond the bounds still produce change notifications.
type Tracker struct {
	mu      sync.Mutex
	ws      *workspace.Workspace
	maxFile int64
	budget  int64
	diffCap int
	snaps   map[string]*snapshot
	order   []string
	total   int64
	seeded  bool
}

type snapshot struct {
	data []byte
	hash [sha256.Size]byte
}

// NewTracker creates a tracker. maxSnapshot is the largest file retained,
// budget bounds total retained bytes, and diffCap bounds Myers complexity.
func NewTracker(ws *workspace.Workspace, maxSnapshot, budget int64, diffCap int) *Tracker {
	return &Tracker{
		ws:      ws,
		maxFile: maxSnapshot,
		budget:  budget,
		diffCap: diffCap,
		snaps:   make(map[string]*snapshot),
	}
}

// Seed walks the workspace and records current text files. It is safe to call
// concurrently with Apply; an existing snapshot is never overwritten by Seed.
func (t *Tracker) Seed(ctx context.Context) {
	err := t.ws.WalkFiles(func(rel string, size int64) bool {
		if ctx.Err() != nil {
			return false
		}
		if size <= 0 || size > t.maxFile {
			return true
		}
		abs, err := t.ws.Resolve(rel)
		if err != nil {
			return true
		}
		b, err := os.ReadFile(abs)
		if err != nil || workspace.IsBinary(b) {
			return true
		}
		t.storeIfAbsent(rel, b)
		return true
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("watch: seed: %v", err)
	}
	t.mu.Lock()
	t.seeded = true
	t.mu.Unlock()
}

func (t *Tracker) storeIfAbsent(rel string, b []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.snaps[rel]; ok {
		return
	}
	t.storeLocked(rel, b)
}

// Observe records content that was read by another layer (for example the
// file API) so the next agent write can be diffed against it.
func (t *Tracker) Observe(rel string, b []byte) {
	if int64(len(b)) > t.maxFile || workspace.IsBinary(b) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.snaps[rel]; ok && old.hash == sha256.Sum256(b) {
		return
	}
	t.storeLocked(rel, b)
}

func (t *Tracker) storeLocked(rel string, b []byte) {
	if int64(len(b)) > t.maxFile {
		return
	}
	sum := sha256.Sum256(b)
	if old, ok := t.snaps[rel]; ok {
		t.total -= int64(len(old.data))
		delete(t.snaps, rel)
	}
	t.snaps[rel] = &snapshot{data: append([]byte(nil), b...), hash: sum}
	t.order = append(t.order, rel)
	t.total += int64(len(b))
	t.evictLocked()
}

func (t *Tracker) evictLocked() {
	for t.total > t.budget && len(t.order) > 0 {
		rel := t.order[0]
		t.order = t.order[1:]
		s, ok := t.snaps[rel]
		if !ok {
			continue
		}
		t.total -= int64(len(s.data))
		delete(t.snaps, rel)
	}
	// Drop stale order entries that point at currently stored snapshots only
	// once the queue grows unreasonably; lookups tolerate duplicates.
	if len(t.order) > 4*len(t.snaps)+1024 {
		t.compactOrderLocked()
	}
}

func (t *Tracker) compactOrderLocked() {
	seen := make(map[string]struct{}, len(t.snaps))
	for rel := range t.snaps {
		seen[rel] = struct{}{}
	}
	out := t.order[:0]
	kept := make(map[string]struct{}, len(t.snaps))
	for _, rel := range t.order {
		if _, ok := seen[rel]; !ok {
			continue
		}
		if _, dup := kept[rel]; dup {
			continue
		}
		kept[rel] = struct{}{}
		out = append(out, rel)
	}
	t.order = out
}

// Forget drops a snapshot (used when a file is deleted).
func (t *Tracker) Forget(rel string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.snaps[rel]; ok {
		t.total -= int64(len(s.data))
		delete(t.snaps, rel)
	}
}

// Seeded reports whether the initial background scan completed.
func (t *Tracker) Seeded() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.seeded
}

// Stats reports snapshot retention.
func (t *Tracker) Stats() (files int, bytes int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.snaps), t.total
}

// Apply inspects the current state of rel and returns a change if the content
// differs from the last snapshot. It is the single entry point used by the
// watcher and by the initial directory scan.
func (t *Tracker) Apply(rel string) (Change, bool) {
	if t.ws.IsIgnored(rel) {
		return Change{}, false
	}
	abs, err := t.ws.Resolve(rel)
	if err != nil {
		return Change{}, false
	}
	ch := Change{Path: rel, At: time.Now().UTC(), Language: workspace.Language(rel)}
	st, statErr := os.Stat(abs)
	if statErr != nil {
		ch.Kind = "deleted"
		t.mu.Lock()
		old, ok := t.snaps[rel]
		if ok {
			delete(t.snaps, rel)
			t.total -= int64(len(old.data))
		}
		t.mu.Unlock()
		if ok {
			ch.Hunks, ch.Stats = diff.Compute(old.data, nil, t.diffCap)
		}
		return ch, true
	}
	if st.IsDir() {
		ch.Kind = "directory"
		ch.IsDir = true
		return ch, true
	}
	if !st.Mode().IsRegular() {
		return Change{}, false
	}
	ch.Size = st.Size()
	if st.Size() > t.maxFile {
		t.Forget(rel)
		ch.Kind = "modified"
		return ch, true
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return Change{}, false
	}
	if workspace.IsBinary(b) {
		t.Forget(rel)
		ch.Kind = "modified"
		ch.Binary = true
		return ch, true
	}
	t.mu.Lock()
	old, hadOld := t.snaps[rel]
	var oldData []byte
	if hadOld {
		oldData = old.data
		if old.hash == sha256.Sum256(b) {
			t.mu.Unlock()
			return Change{}, false
		}
	}
	t.storeLocked(rel, b)
	t.mu.Unlock()

	ch.Kind = "modified"
	if !hadOld {
		ch.Kind = "created"
	}
	hunks, stats := diff.Compute(oldData, b, t.diffCap)
	ch.Stats = stats
	if stats.Added+stats.Removed > 2000 {
		ch.HunksOmitted = true
	} else {
		ch.Hunks = hunks
	}
	return ch, true
}

// Watcher is a recursive fsnotify watcher with debouncing.
type Watcher struct {
	fsw      *fsnotify.Watcher
	ws       *workspace.Workspace
	tracker  *Tracker
	debounce time.Duration
	onChange func(Change)

	mu    sync.Mutex
	dirs  map[string]bool
	ready chan struct{}
}

// New starts the watcher and registers every non-ignored directory. The
// returned watcher must be closed.
func New(ws *workspace.Workspace, tracker *Tracker, debounce time.Duration, onChange func(Change)) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		fsw:      fsw,
		ws:       ws,
		tracker:  tracker,
		debounce: debounce,
		onChange: onChange,
		dirs:     make(map[string]bool),
		ready:    make(chan struct{}),
	}
	if err := w.addTree("", false); err != nil {
		fsw.Close()
		return nil, err
	}
	close(w.ready)
	return w, nil
}

// Ready is closed once initial watches are registered.
func (w *Watcher) Ready() <-chan struct{} { return w.ready }

func (w *Watcher) addTree(rel string, emitFiles bool) error {
	abs, err := w.ws.Resolve(rel)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	if err := w.fsw.Add(abs); err != nil {
		return err
	}
	w.mu.Lock()
	w.dirs[rel] = true
	w.mu.Unlock()
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil
	}
	for _, de := range entries {
		childRel := pathpkg.Join(rel, de.Name())
		if w.ws.IsIgnored(childRel) {
			continue
		}
		if de.IsDir() {
			if err := w.addTree(childRel, emitFiles); err != nil {
				continue
			}
		} else if emitFiles {
			if ch, ok := w.tracker.Apply(childRel); ok {
				w.onChange(ch)
			}
		}
	}
	return nil
}

// Close stops watching.
func (w *Watcher) Close() error { return w.fsw.Close() }

// Run debounces filesystem events until ctx is done.
func (w *Watcher) Run(ctx context.Context) {
	pending := make(map[string]fsnotify.Op)
	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			rel, ok := w.ws.Rel(ev.Name)
			if !ok {
				continue
			}
			if rel != "" && w.ws.IsIgnored(rel) {
				continue
			}
			if ev.Op&fsnotify.Chmod != 0 && ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			if ev.Op&fsnotify.Create != 0 {
				abs := ev.Name
				if st, err := os.Stat(abs); err == nil && st.IsDir() {
					_ = w.addTree(rel, true)
				}
			}
			pending[rel] |= ev.Op
			if timer == nil {
				timer = time.NewTimer(w.debounce)
				timerC = timer.C
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(w.debounce)
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			log.Printf("watch: %v", err)
		case <-timerC:
			timerC = nil
			timer = nil
			paths := make([]string, 0, len(pending))
			for p := range pending {
				paths = append(paths, p)
			}
			pending = make(map[string]fsnotify.Op)
			sort.Strings(paths)
			for _, p := range paths {
				w.emit(p)
			}
		}
	}
}

func (w *Watcher) emit(rel string) {
	w.mu.Lock()
	wasDir := w.dirs[rel]
	w.mu.Unlock()
	if wasDir {
		abs, err := w.ws.Resolve(rel)
		if err != nil {
			return
		}
		if _, statErr := os.Stat(abs); statErr != nil {
			w.mu.Lock()
			delete(w.dirs, rel)
			w.mu.Unlock()
			w.onChange(Change{
				Path:  rel,
				Kind:  "directory",
				IsDir: true,
				At:    time.Now().UTC(),
			})
			return
		}
	}
	if ch, ok := w.tracker.Apply(rel); ok {
		w.onChange(ch)
	}
}
