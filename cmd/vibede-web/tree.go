//go:build js && wasm

package main

import (
	"path"
	"strings"
	"syscall/js"
	"time"
)

func (a *app) renderTreeLocked(root string) {
	clear(a.els.tree)
	a.renderTreeInto(a.els.tree, root, 0)
}

func (a *app) renderTreeInto(parent js.Value, dir string, depth int) {
	entries, ok := a.tree[dir]
	if !ok {
		if dir == "" {
			note := el("div", "tree-empty")
			setText(note, "No files loaded.")
			add(parent, note)
		}
		return
	}
	if len(entries) == 0 {
		if depth == 0 {
			note := el("div", "tree-empty")
			setText(note, "Empty directory.")
			add(parent, note)
		}
		return
	}
	for _, e := range entries {
		row := el("div", "tree-row")
		if e.IsDir {
			row.Set("className", "tree-row dir")
		}
		if e.Path == a.focusedPath() {
			row.Set("className", row.Get("className").String()+" active")
		}
		twisty := el("span", "twisty")
		if e.IsDir {
			if a.expanded[e.Path] {
				setText(twisty, "▾")
			} else {
				setText(twisty, "▸")
			}
		}
		icon := el("span", "file-icon")
		if e.IsDir {
			setText(icon, "▣")
		} else {
			setText(icon, fileIcon(e.Language))
		}
		name := el("span", "name")
		setText(name, e.Name)
		add(row, twisty, icon, name)
		if !e.IsDir {
			if fd, ok := a.diags[e.Path]; ok && (fd.Counts.Errors > 0 || fd.Counts.Warnings > 0) {
				dot := el("span", "badge-dot")
				if fd.Counts.Errors == 0 {
					dot.Set("style", "background:var(--yellow)")
				}
				add(row, dot)
			}
			if e.Size > 0 {
				meta := el("span", "tmeta")
				setText(meta, compactNum(e.Size))
				add(row, meta)
			}
		}
		row.Set("title", e.Path)
		entry := e
		on(row, "click", func(ev js.Value) {
			if entry.IsDir {
				go a.toggleDir(entry.Path)
			} else {
				go a.openFile(entry.Path, 0, 0, false)
			}
		})
		add(parent, row)
		if e.IsDir && a.expanded[e.Path] {
			children := el("div", "tree-children")
			a.renderTreeInto(children, e.Path, depth+1)
			add(parent, children)
		}
	}
}

func (a *app) toggleDir(dir string) {
	a.mu.Lock()
	if a.expanded[dir] {
		a.expanded[dir] = false
		a.renderTreeLocked("")
		a.mu.Unlock()
		return
	}
	a.expanded[dir] = true
	cached := a.tree[dir] != nil
	if cached {
		a.renderTreeLocked("")
	}
	a.mu.Unlock()
	if !cached {
		entries, err := fetchTree(dir)
		if err != nil {
			a.toast("Cannot read "+dir+": "+apiErrorHint(err), "err")
			return
		}
		a.mu.Lock()
		a.tree[dir] = entries
		a.renderTreeLocked("")
		a.mu.Unlock()
	}
}

func (a *app) reloadTree() {
	var dirs []string
	a.mu.Lock()
	dirs = append(dirs, "")
	for d, open := range a.expanded {
		if open && d != "" {
			dirs = append(dirs, d)
		}
	}
	a.mu.Unlock()
	type result struct {
		dir     string
		entries []Entry
		err     error
	}
	ch := make(chan result, len(dirs))
	for _, d := range dirs {
		go func(dir string) {
			entries, err := fetchTree(dir)
			ch <- result{dir: dir, entries: entries, err: err}
		}(d)
	}
	a.mu.Lock()
	for range dirs {
		r := <-ch
		if r.err == nil {
			a.tree[r.dir] = r.entries
		}
	}
	a.renderTreeLocked("")
	a.mu.Unlock()
}

func (a *app) onFilterChanged() {
	query := strings.TrimSpace(a.els.search.Get("value").String())
	a.mu.Lock()
	a.filterGen++
	gen := a.filterGen
	a.mu.Unlock()
	if query == "" {
		a.mu.Lock()
		a.renderTreeLocked("")
		a.mu.Unlock()
		return
	}
	go func() {
		time.Sleep(180 * time.Millisecond)
		a.mu.Lock()
		stale := gen != a.filterGen
		a.mu.Unlock()
		if stale {
			return
		}
		results, err := searchFiles(query)
		if err != nil {
			a.toast("Search failed: "+apiErrorHint(err), "err")
			return
		}
		a.mu.Lock()
		if gen != a.filterGen {
			a.mu.Unlock()
			return
		}
		a.renderSearchResultsLocked(query, results)
		a.mu.Unlock()
	}()
}

func (a *app) renderSearchResultsLocked(query string, results []Entry) {
	clear(a.els.tree)
	wrapper := el("div", "search-results")
	if len(results) == 0 {
		note := el("div", "tree-empty")
		setText(note, "No files match "+query)
		add(wrapper, note)
	}
	for _, r := range results {
		row := el("div", "tree-row")
		if r.Path == a.focusedPath() {
			row.Set("className", "tree-row active")
		}
		icon := el("span", "file-icon")
		setText(icon, fileIcon(r.Language))
		name := el("span", "name")
		setText(name, r.Name)
		dir := el("span", "tmeta")
		dirName := path.Dir(r.Path)
		if dirName == "." {
			dirName = ""
		}
		setText(dir, dirName)
		add(row, el("span", "twisty"), icon, name, dir)
		row.Set("title", r.Path)
		p := r.Path
		on(row, "click", func(js.Value) { go a.openFile(p, 0, 0, false) })
		add(wrapper, row)
	}
	add(a.els.tree, wrapper)
}

// refreshTreeFor reloads the directory containing p; used for create/delete.
func (a *app) refreshTreeFor(p string) {
	dir := path.Dir(p)
	if dir == "." {
		dir = ""
	}
	a.mu.Lock()
	_, cached := a.tree[dir]
	open := a.expanded[dir] || dir == ""
	query := strings.TrimSpace(a.els.search.Get("value").String())
	a.mu.Unlock()
	if !cached && !open {
		return
	}
	if query != "" {
		return
	}
	entries, err := fetchTree(dir)
	if err != nil {
		return
	}
	a.mu.Lock()
	a.tree[dir] = entries
	if a.expanded[dir] || dir == "" {
		a.renderTreeLocked("")
	}
	a.mu.Unlock()
}
