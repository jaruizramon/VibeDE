// Package workspace provides read-only access to a code root. Every path that
// reaches the filesystem is resolved through Resolve, which rejects traversal
// and symlinks that escape the root.
package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"vibede/internal/config"
)

// ErrOutsideRoot is returned for a path that escapes the workspace.
var ErrOutsideRoot = errors.New("path escapes workspace")

// ErrBinary is returned when a file is not valid UTF-8 text.
var ErrBinary = errors.New("binary file")

// Entry is one directory item.
type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	IsDir    bool      `json:"isDir"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified,omitempty"`
	Language string    `json:"language,omitempty"`
	Symlink  bool      `json:"symlink,omitempty"`
}

// Workspace is a read-only view of a directory tree.
type Workspace struct {
	root    string
	ignore  []string
	maxFile int64
}

// New builds a workspace from resolved configuration.
func New(cfg config.Config) *Workspace {
	return &Workspace{root: cfg.Root, ignore: cfg.Ignore, maxFile: cfg.Limits.MaxFileBytes}
}

// Root returns the absolute workspace root.
func (w *Workspace) Root() string { return w.root }

// MaxFileBytes returns the largest file the HTTP layer will serve.
func (w *Workspace) MaxFileBytes() int64 { return w.maxFile }

// Resolve converts a relative, slash-separated path to an absolute path inside
// the root. Symlinks are resolved and must also stay inside the root.
func (w *Workspace) Resolve(rel string) (string, error) {
	if strings.HasPrefix(rel, "/") {
		return "", ErrOutsideRoot
	}
	rel = path.Clean(rel)
	if rel == "." {
		rel = ""
	}
	if rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return "", ErrOutsideRoot
	}
	abs := filepath.Join(w.root, filepath.FromSlash(rel))
	if !within(w.root, abs) {
		return "", ErrOutsideRoot
	}
	// Resolve symlinks on the existing path (or its nearest existing parent)
	// so a link cannot smuggle a path outside the root.
	probe := abs
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if !within(w.root, resolved) {
				return "", ErrOutsideRoot
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	return abs, nil
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// Rel converts an absolute path to a slash-relative path inside the workspace.
func (w *Workspace) Rel(abs string) (string, bool) {
	if !within(w.root, abs) {
		return "", false
	}
	rel, err := filepath.Rel(w.root, abs)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return filepath.ToSlash(rel), true
}

// IsIgnored reports whether a relative path matches an ignored directory or
// glob pattern. Globs are matched against the full relative path and basename.
func (w *Workspace) IsIgnored(rel string) bool {
	rel = strings.TrimPrefix(path.Clean(rel), "./")
	if rel == "." {
		return false
	}
	base := path.Base(rel)
	for _, pat := range w.ignore {
		if pat == "" {
			continue
		}
		if strings.ContainsAny(pat, "*?[") {
			if ok, _ := path.Match(pat, rel); ok {
				return true
			}
			if ok, _ := path.Match(pat, base); ok {
				return true
			}
			continue
		}
		if base == pat {
			return true
		}
		if rel == pat || strings.HasPrefix(rel, pat+"/") {
			return true
		}
	}
	return false
}

// hiddenByPath reports whether any segment of rel is ignored. This catches
// walking into ignored ancestors even when only the leaf was checked.
func (w *Workspace) hiddenByPath(rel string) bool {
	if rel == "" {
		return false
	}
	if w.IsIgnored(rel) {
		return true
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		if w.IsIgnored(strings.Join(parts[:i+1], "/")) {
			return true
		}
	}
	return false
}

// List returns directory entries, directories first and then name order.
// Ignored entries are omitted.
func (w *Workspace) List(rel string) ([]Entry, error) {
	abs, err := w.Resolve(rel)
	if err != nil {
		return nil, err
	}
	if w.hiddenByPath(rel) {
		return nil, nil
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", rel)
	}
	dirents, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(dirents))
	for _, de := range dirents {
		name := de.Name()
		childRel := path.Join(rel, name)
		if w.IsIgnored(childRel) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		e := Entry{
			Name:     name,
			Path:     childRel,
			IsDir:    info.IsDir(),
			Size:     info.Size(),
			Modified: info.ModTime().UTC(),
			Symlink:  info.Mode()&fs.ModeSymlink != 0,
		}
		if de.Type()&fs.ModeSymlink != 0 {
			// Lstat via DirEntry reports the link itself; only follow if it
			// stays in the workspace and points at a directory.
			if target, err := w.Resolve(childRel); err == nil {
				if tst, err := os.Stat(target); err == nil && tst.IsDir() {
					e.IsDir = true
				}
			}
		}
		if !e.IsDir {
			e.Language = Language(childRel)
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Stat returns metadata for a single path.
func (w *Workspace) Stat(rel string) (Entry, error) {
	abs, err := w.Resolve(rel)
	if err != nil {
		return Entry{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return Entry{}, err
	}
	e := Entry{
		Name:     path.Base(rel),
		Path:     rel,
		IsDir:    st.IsDir(),
		Size:     st.Size(),
		Modified: st.ModTime().UTC(),
	}
	if rel == "" {
		e.Name = filepath.Base(w.root)
		e.Path = ""
	}
	if !e.IsDir {
		e.Language = Language(rel)
	}
	return e, nil
}

// Read returns file bytes. Directories, oversized files, binary files and
// paths outside the root are rejected.
func (w *Workspace) Read(rel string) ([]byte, error) {
	abs, err := w.Resolve(rel)
	if err != nil {
		return nil, err
	}
	if w.hiddenByPath(rel) {
		return nil, fs.ErrNotExist
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a directory", rel)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	if st.Size() > w.maxFile {
		return nil, fmt.Errorf("%s exceeds the %d byte read limit", rel, w.maxFile)
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	if IsBinary(b) {
		return nil, ErrBinary
	}
	return b, nil
}

// IsBinary detects NUL bytes in the first 8 KiB, the same heuristic editors
// use. It does not attempt to validate UTF-8.
func IsBinary(b []byte) bool {
	n := len(b)
	if n > 8192 {
		n = 8192
	}
	return bytes.IndexByte(b[:n], 0) >= 0
}

// WalkFiles visits every non-ignored regular file under the root. Returning
// false from fn stops the walk. Symlinked directories are not followed.
func (w *Workspace) WalkFiles(fn func(rel string, size int64) bool) error {
	return filepath.WalkDir(w.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, ok := w.Rel(p)
		if !ok {
			return nil
		}
		if rel != "" && w.IsIgnored(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if !fn(rel, info.Size()) {
			return filepath.SkipAll
		}
		return nil
	})
}
