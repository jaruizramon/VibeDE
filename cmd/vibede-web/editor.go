//go:build js && wasm

package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall/js"
	"time"
	"unicode/utf16"

	"vibede/internal/highlight"
)

func (a *app) paneAt(i int) *pane {
	if i < 0 || i >= len(a.panes) {
		return nil
	}
	return a.panes[i]
}

func (a *app) focusedPane() *pane { return a.paneAt(a.activePane) }

func (a *app) activeTab() *tab {
	p := a.focusedPane()
	if p == nil || p.active == "" {
		return nil
	}
	return p.tabs[p.active]
}

func (a *app) focusedPath() string {
	if t := a.activeTab(); t != nil {
		return t.Path
	}
	return ""
}

func (a *app) isOpen(path string) bool {
	for _, p := range a.panes {
		if p.tabs[path] != nil {
			return true
		}
	}
	return false
}

func (a *app) paneVisible(i int) bool { return i == 0 || a.split }

func (a *app) seqKey(pane int, path string) string {
	return strconv.Itoa(pane) + "|" + path
}

func (a *app) applyWorkspaceClassLocked() {
	if !a.els.workspace.Truthy() {
		return
	}
	cls := "workspace"
	if a.detached {
		cls += " detached"
	}
	a.els.workspace.Set("className", cls)
}

func (a *app) applyPaneFocusLocked() {
	for _, p := range a.panes {
		if p == nil || !p.root.Truthy() {
			continue
		}
		cls := "editor-pane"
		if p.idx == a.activePane {
			cls += " focused"
		}
		p.root.Set("className", cls)
	}
}

func (a *app) applySplitLayoutLocked() {
	if len(a.panes) < 2 || !a.els.paneSplitter.Truthy() {
		return
	}
	p0, p1 := a.panes[0], a.panes[1]
	if a.split && !a.detached {
		a.els.panes.Set("className", "panes split")
		a.els.paneSplitter.Set("style", "")
		p0.root.Set("style", fmt.Sprintf("flex:0 0 %.2f%%", a.paneRatio*100))
		p1.root.Set("style", "flex:1 1 0")
	} else {
		if a.els.panes.Truthy() {
			a.els.panes.Set("className", "panes")
		}
		a.els.paneSplitter.Set("style", "display:none")
		p0.root.Set("style", "flex:1 1 0")
		p1.root.Set("style", "display:none")
	}
	a.applyPaneFocusLocked()
}

func (a *app) applyRightWidthLocked() {
	if !a.els.workspace.Truthy() || a.detached {
		return
	}
	w := a.rightWidth
	if w < 280 {
		w = 280
	}
	if max := js.Global().Get("innerWidth").Int() - 420; max > 280 && w > max {
		w = max
	}
	a.rightWidth = w
	a.els.workspace.Get("style").Call("setProperty", "--right", strconv.Itoa(w)+"px")
}

func (a *app) ensureDiagramWidthLocked() {
	if a.detached {
		return
	}
	if a.rightWidth < 560 {
		a.rightWidth = 620
		a.applyRightWidthLocked()
	}
}

func (a *app) saveScroll(idx int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.paneAt(idx)
	if p == nil || p.active == "" {
		return
	}
	if t := p.tabs[p.active]; t != nil {
		t.Scroll = p.scroll.Get("scrollTop").Int()
	}
}

func (a *app) focusPane(idx int) {
	a.mu.Lock()
	a.setActivePaneLocked(idx)
	a.mu.Unlock()
}

func (a *app) setActivePaneLocked(idx int) {
	p := a.paneAt(idx)
	if p == nil || !a.paneVisible(idx) {
		return
	}
	a.activePane = idx
	a.applyPaneFocusLocked()
	a.updateTreeActiveLocked()
	a.renderOutlineLocked()
	a.renderRightTabs()
	a.renderRight()
	a.renderStatus()
	if a.rightTab == "diagrams" {
		a.loadDiagramLocked(false)
	}
}

func (a *app) beginPaneDrag(ev js.Value) {
	if !a.split || a.detached {
		return
	}
	ev.Call("preventDefault")
	rect := a.els.panes.Call("getBoundingClientRect")
	left := rect.Get("left").Float()
	width := rect.Get("width").Float()
	if width <= 0 {
		return
	}
	doc().Get("body").Get("style").Set("userSelect", "none")
	move := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		x := args[0].Get("clientX").Float()
		ratio := (x - left) / width
		if ratio < 0.2 {
			ratio = 0.2
		}
		if ratio > 0.8 {
			ratio = 0.8
		}
		a.mu.Lock()
		a.paneRatio = ratio
		a.applySplitLayoutLocked()
		a.mu.Unlock()
		return nil
	})
	var up js.Func
	up = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		doc().Call("removeEventListener", "pointermove", move)
		doc().Call("removeEventListener", "pointerup", up)
		doc().Get("body").Get("style").Set("userSelect", "")
		move.Release()
		up.Release()
		return nil
	})
	doc().Call("addEventListener", "pointermove", move)
	doc().Call("addEventListener", "pointerup", up)
}

func (a *app) beginRightDrag(ev js.Value) {
	ev.Call("preventDefault")
	startX := ev.Get("clientX").Float()
	a.mu.Lock()
	startWidth := a.rightWidth
	a.mu.Unlock()
	doc().Get("body").Get("style").Set("userSelect", "none")
	move := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		dx := args[0].Get("clientX").Float() - startX
		w := startWidth - int(dx)
		if w < 280 {
			w = 280
		}
		if max := js.Global().Get("innerWidth").Int() - 420; max > 280 && w > max {
			w = max
		}
		a.mu.Lock()
		a.rightWidth = w
		a.applyRightWidthLocked()
		a.mu.Unlock()
		return nil
	})
	var up js.Func
	up = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		doc().Call("removeEventListener", "pointermove", move)
		doc().Call("removeEventListener", "pointerup", up)
		doc().Get("body").Get("style").Set("userSelect", "")
		move.Release()
		up.Release()
		return nil
	})
	doc().Call("addEventListener", "pointermove", move)
	doc().Call("addEventListener", "pointerup", up)
}

func (a *app) toggleSplit() {
	var dupPath string
	var dupLine, dupChar int
	a.mu.Lock()
	if a.detached {
		a.mu.Unlock()
		return
	}
	if !a.split {
		a.split = true
		a.activePane = 1
		a.applySplitLayoutLocked()
		a.renderTabsLocked(0)
		a.renderTabsLocked(1)
		a.renderToolbarLocked(0)
		a.renderToolbarLocked(1)
		if p1 := a.panes[1]; p1.active != "" {
			a.renderEditorLocked(1)
			a.scrollToLineLocked(1, p1.tabs[p1.active].Line)
		} else if t0 := a.panes[0].tabs[a.panes[0].active]; t0 != nil {
			dupPath, dupLine, dupChar = t0.Path, t0.Line, t0.Char
		} else {
			a.renderEditorLocked(1)
		}
		a.renderOutlineLocked()
		a.renderRightTabs()
		a.renderRight()
		a.renderStatus()
		a.updateTreeActiveLocked()
		a.mu.Unlock()
		if dupPath != "" {
			go a.openFileInPane(1, dupPath, dupLine, dupChar, nil)
		}
		return
	}
	a.split = false
	a.activePane = 0
	a.applySplitLayoutLocked()
	a.renderToolbarLocked(0)
	a.renderToolbarLocked(1)
	a.renderOutlineLocked()
	a.renderRightTabs()
	a.renderRight()
	a.renderStatus()
	a.updateTreeActiveLocked()
	a.mu.Unlock()
}

func (a *app) detachPane(idx int) {
	a.mu.Lock()
	p := a.paneAt(idx)
	if p == nil || p.active == "" {
		a.mu.Unlock()
		a.toast("No file to detach", "warn")
		return
	}
	t := p.tabs[p.active]
	path, line := t.Path, t.Line
	a.mu.Unlock()
	go func() {
		err := apiPost("/api/detach", map[string]any{"path": path, "line": line + 1}, nil)
		if err != nil {
			a.toast("Detach failed: "+apiErrorHint(err), "err")
			return
		}
		a.toast("Detached "+baseName(path)+" into a new window", "")
	}()
}

// openFile opens in the focused pane. The focus argument is retained for call
// sites that historically distinguished navigation from background opens;
// opens are always focused now.
func (a *app) openFile(p string, line, char int, focus bool) {
	_ = focus
	a.openFileInPane(a.activePane, p, line, char, nil)
}

func (a *app) openFileInPane(idx int, p string, line, char int, highlight map[int]string) {
	a.mu.Lock()
	pane := a.paneAt(idx)
	if pane == nil {
		a.mu.Unlock()
		return
	}
	if idx == 1 {
		a.split = true
		a.applySplitLayoutLocked()
	}
	a.activePane = idx
	pane.active = p
	a.applyPaneFocusLocked()
	key := a.seqKey(idx, p)
	if t := pane.tabs[p]; t != nil {
		if t.Err != "" {
			t.Err = ""
			t.Loading = true
			seq := a.openSeq[key] + 1
			a.openSeq[key] = seq
			a.renderTabsLocked(idx)
			a.renderToolbarLocked(idx)
			a.renderEditorLocked(idx)
			a.mu.Unlock()
			go a.loadTab(idx, p, seq, line, char)
			return
		}
		t.Line = line
		t.Char = char
		t.Highlight = highlight
		a.updateTreeActiveLocked()
		a.renderTabsLocked(idx)
		a.renderToolbarLocked(idx)
		a.renderEditorLocked(idx)
		a.scrollToLineLocked(idx, line)
		a.renderOutlineLocked()
		a.renderRightTabs()
		a.renderRight()
		a.renderStatus()
		if a.rightTab == "diagrams" {
			a.loadDiagramLocked(false)
		}
		a.mu.Unlock()
		return
	}
	seq := a.openSeq[key] + 1
	a.openSeq[key] = seq
	t := &tab{Path: p, Loading: true, Line: line, Char: char, Highlight: highlight}
	pane.tabs[p] = t
	pane.order = append(pane.order, p)
	a.updateTreeActiveLocked()
	a.renderTabsLocked(idx)
	a.renderToolbarLocked(idx)
	a.renderEditorLocked(idx)
	a.renderOutlineLocked()
	a.renderRightTabs()
	a.renderRight()
	a.renderStatus()
	if a.rightTab == "diagrams" {
		a.loadDiagramLocked(false)
	}
	a.mu.Unlock()
	go a.loadTab(idx, p, seq, line, char)
}

func (a *app) loadTab(paneIdx int, p string, seq, line, char int) {
	go func() {
		err := openDocument(p)
		if err != nil {
			a.mu.Lock()
			key := apiErrorInstall(err)
			if key == "" {
				key = apiErrorHint(err)
			}
			if !a.warned[key] {
				a.warned[key] = true
				a.toast("Language server: "+apiErrorHint(err), "warn")
			}
			a.mu.Unlock()
		}
	}()
	f, err := fetchFile(p)
	if err != nil {
		a.finishTabError(paneIdx, p, seq, err)
		return
	}
	an, anErr := fetchAnalysis(p)
	if anErr != nil {
		an = Analysis{Path: p, Language: f.Language, Lines: f.Lines, Metrics: Metrics{DominantBigO: "—"}}
		an.Notes = []string{"Analysis failed: " + apiErrorHint(anErr)}
	}
	diag, _ := fetchDiagnostics(p)
	lines := splitLines(f.Content)
	tokens := highlight.Tokenize(f.Content, f.Language)

	a.mu.Lock()
	pane := a.paneAt(paneIdx)
	if pane == nil {
		a.mu.Unlock()
		return
	}
	t := pane.tabs[p]
	if t == nil || a.openSeq[a.seqKey(paneIdx, p)] != seq {
		a.mu.Unlock()
		return
	}
	t.File = f
	t.Analysis = an
	t.Diags = diag
	t.Tokens = tokens
	t.LinesText = lines
	t.Loading = false
	t.Loaded = true
	t.Err = ""
	t.Line = line
	t.Char = char
	if len(diag.Diagnostics) > 0 {
		a.diags[p] = diag
	} else {
		delete(a.diags, p)
	}
	a.renderTabsLocked(paneIdx)
	a.renderToolbarLocked(paneIdx)
	if pane.active == p {
		a.renderEditorLocked(paneIdx)
		a.scrollToLineLocked(paneIdx, line)
	}
	if a.activePane == paneIdx {
		a.renderOutlineLocked()
		a.renderStatus()
	}
	a.renderRightTabs()
	a.renderRight()
	if a.activePane == paneIdx && a.rightTab == "diagrams" {
		a.loadDiagramLocked(false)
	}
	a.mu.Unlock()
}

func (a *app) finishTabError(paneIdx int, p string, seq int, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pane := a.paneAt(paneIdx)
	if pane == nil {
		return
	}
	t := pane.tabs[p]
	if t == nil || a.openSeq[a.seqKey(paneIdx, p)] != seq {
		return
	}
	t.Loading = false
	t.Loaded = true
	t.Err = apiErrorHint(err)
	a.renderTabsLocked(paneIdx)
	a.renderToolbarLocked(paneIdx)
	if pane.active == p {
		a.renderEditorLocked(paneIdx)
	}
	if a.activePane == paneIdx {
		a.renderStatus()
	}
}

func (a *app) refreshTab(p string) {
	a.mu.Lock()
	if !a.isOpen(p) {
		a.mu.Unlock()
		return
	}
	if a.refreshBusy[p] {
		a.refreshOn[p] = true
		a.mu.Unlock()
		return
	}
	a.refreshBusy[p] = true
	a.mu.Unlock()
	for {
		f, err := fetchFile(p)
		an, anErr := Analysis{}, error(nil)
		diag := FileDiagnostics{}
		if err == nil {
			an, anErr = fetchAnalysis(p)
			if anErr == nil {
				diag, _ = fetchDiagnostics(p)
			}
		}
		a.mu.Lock()
		for _, pane := range a.panes {
			t := pane.tabs[p]
			if t == nil {
				continue
			}
			if err != nil {
				t.Loading = false
				t.Err = "File changed or was removed: " + apiErrorHint(err)
			} else {
				if pane.active == p {
					t.Scroll = pane.scroll.Get("scrollTop").Int()
				}
				t.File = f
				t.LinesText = splitLines(f.Content)
				t.Tokens = highlight.Tokenize(f.Content, f.Language)
				if anErr == nil {
					t.Analysis = an
				}
				t.Diags = diag
				t.Err = ""
				t.Loaded = true
				t.Loading = false
				t.Highlight = nil
			}
			a.renderTabsLocked(pane.idx)
			if pane.active == p {
				a.renderToolbarLocked(pane.idx)
				a.renderEditorLocked(pane.idx)
			}
		}
		if len(diag.Diagnostics) > 0 {
			a.diags[p] = diag
		} else {
			delete(a.diags, p)
		}
		if a.activePath() == p {
			a.renderOutlineLocked()
			a.renderStatus()
			if a.rightTab == "diagrams" {
				a.loadDiagramLocked(false)
			}
			a.renderRight()
		}
		a.renderRightTabs()
		again := a.refreshOn[p]
		a.refreshOn[p] = false
		a.refreshBusy[p] = false
		a.mu.Unlock()
		if !again {
			return
		}
	}
}

func splitLines(s string) []string {
	if s == "" {
		return []string{""}
	}
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func (a *app) renderTabsLocked(paneIdx int) {
	pane := a.paneAt(paneIdx)
	if pane == nil || !pane.tabsEl.Truthy() {
		return
	}
	clear(pane.tabsEl)
	for _, p := range pane.order {
		t := pane.tabs[p]
		if t == nil {
			continue
		}
		tabEl := el("div", "tab")
		if p == pane.active {
			tabEl.Set("className", "tab active")
		}
		name := el("span", "tname")
		setText(name, baseName(p))
		tabEl.Set("title", p)
		add(tabEl, name)
		if fd, ok := a.diags[p]; ok && (fd.Counts.Errors > 0 || fd.Counts.Warnings > 0) {
			dot := el("span", "diag-dot")
			if fd.Counts.Errors == 0 {
				dot.Set("className", "diag-dot warn")
			}
			add(tabEl, dot)
		}
		if t.Highlight != nil {
			isSearch := false
			for _, v := range t.Highlight {
				if v == "search" {
					isSearch = true
					break
				}
			}
			d := el("span", "tab-diff")
			title := "Opened from an activity diff"
			if isSearch {
				d.Set("className", "tab-diff search")
				title = "Jumped from code search"
			}
			d.Set("title", title)
			add(tabEl, d)
		}
		closeBtn := el("button", "close")
		setText(closeBtn, "×")
		closeBtn.Set("title", "Close tab")
		path := p
		on(closeBtn, "click", func(ev js.Value) {
			ev.Call("stopPropagation")
			a.closeTab(paneIdx, path)
		})
		add(tabEl, closeBtn)
		on(tabEl, "click", func(js.Value) { go a.activateTab(paneIdx, path) })
		add(pane.tabsEl, tabEl)
	}
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func dirName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

func (a *app) closeTab(paneIdx int, p string) {
	a.mu.Lock()
	pane := a.paneAt(paneIdx)
	if pane == nil {
		a.mu.Unlock()
		return
	}
	wasActive := p == pane.active
	idx := 0
	for i, q := range pane.order {
		if q == p {
			idx = i
			break
		}
	}
	delete(pane.tabs, p)
	out := pane.order[:0]
	for _, q := range pane.order {
		if q != p {
			out = append(out, q)
		}
	}
	pane.order = out
	if wasActive {
		if len(pane.order) == 0 {
			pane.active = ""
		} else {
			if idx >= len(pane.order) {
				idx = len(pane.order) - 1
			}
			pane.active = pane.order[idx]
		}
	}
	a.renderTabsLocked(paneIdx)
	a.renderToolbarLocked(paneIdx)
	a.renderEditorLocked(paneIdx)
	if a.activePane == paneIdx {
		a.renderOutlineLocked()
		a.renderStatus()
		if a.rightTab == "diagrams" {
			a.loadDiagramLocked(false)
		}
		a.renderRight()
	}
	a.updateTreeActiveLocked()
	a.mu.Unlock()
}

func (a *app) activateTab(paneIdx int, p string) {
	a.mu.Lock()
	pane := a.paneAt(paneIdx)
	if pane == nil || pane.tabs[p] == nil {
		a.mu.Unlock()
		return
	}
	a.activePane = paneIdx
	pane.active = p
	a.applyPaneFocusLocked()
	a.updateTreeActiveLocked()
	a.renderTabsLocked(paneIdx)
	a.renderToolbarLocked(paneIdx)
	a.renderEditorLocked(paneIdx)
	t := pane.tabs[p]
	if t != nil {
		a.scrollToLineLocked(paneIdx, t.Line)
	}
	a.renderOutlineLocked()
	a.renderRightTabs()
	a.renderRight()
	a.renderStatus()
	if a.rightTab == "diagrams" {
		a.loadDiagramLocked(false)
	}
	a.mu.Unlock()
}

func (a *app) renderToolbarLocked(paneIdx int) {
	pane := a.paneAt(paneIdx)
	if pane == nil {
		return
	}
	clear(pane.toolbarLeft)
	clear(pane.toolbarRight)
	t := pane.tabs[pane.active]
	if t == nil {
		hint := el("span", "hint")
		setText(hint, "Select a file to begin")
		add(pane.toolbarLeft, hint)
		if !a.detached {
			a.addPaneControlsLocked(paneIdx, pane)
		}
		return
	}
	parts := strings.Split(t.Path, "/")
	for i, part := range parts {
		if i > 0 {
			sep := el("span", "crumb sep")
			setText(sep, "/")
			add(pane.toolbarLeft, sep)
		}
		crumb := el("span", "crumb")
		if i == len(parts)-1 {
			crumb.Set("className", "crumb last")
		}
		setText(crumb, part)
		add(pane.toolbarLeft, crumb)
	}
	addPill := func(class, label, title string) {
		pill := el("span", "pill "+class)
		setText(pill, label)
		if title != "" {
			pill.Set("title", title)
		}
		add(pane.toolbarRight, pill)
	}
	addPill("lang", langName(t.File.Language), "Language")
	if bo := t.Analysis.Metrics.DominantBigO; bo != "" && bo != "—" {
		pill := el("span", "pill good bigo")
		add(pill, text("file "))
		appendBigO(pill, bo)
		pill.Set("title", "Dominant Big-O across functions")
		add(pane.toolbarRight, pill)
	}
	if t.Analysis.Metrics.MaxComplexity > 0 {
		cls := "cc-info"
		if t.Analysis.Metrics.MaxComplexity >= 15 {
			cls += " warn"
		}
		addPill(cls, fmt.Sprintf("max CC %d", t.Analysis.Metrics.MaxComplexity), "Maximum cyclomatic complexity")
	}
	if t.Diags.Counts.Errors > 0 {
		addPill("err issues", fmt.Sprintf("%d errors", t.Diags.Counts.Errors), "Language server errors")
	} else if t.Diags.Counts.Warnings > 0 {
		addPill("warn issues", fmt.Sprintf("%d warnings", t.Diags.Counts.Warnings), "Language server warnings")
	} else {
		addPill("good clean", "no diagnostics", "No diagnostics reported")
	}
	if srv := a.serverForLanguage(t.File.Language); srv != nil {
		cls := "lsp"
		switch srv.Status {
		case "running":
			cls += " good"
		case "missing", "failed":
			cls += " err"
		}
		addPill(cls, "lsp "+srv.Status, srv.Name+" · "+srv.Command)
	}
	if t.Highlight != nil {
		label := "diff"
		for _, v := range t.Highlight {
			if v == "search" {
				label = "match"
				break
			}
		}
		addPill("warn "+label, label, "Highlighted line from Activity or search")
	}
	if a.detached {
		return
	}
	btn := func(label, title string, fn func()) {
		b := el("button", "tool-btn")
		setText(b, label)
		if title != "" {
			b.Set("title", title)
		}
		on(b, "click", func(js.Value) {
			a.mu.Lock()
			a.activePane = paneIdx
			a.applyPaneFocusLocked()
			a.mu.Unlock()
			fn()
		})
		add(pane.toolbarRight, b)
	}
	inspectLabel, diagramLabel, problemsLabel := "Inspect", "Diagram", "Problems"
	if a.split {
		inspectLabel, diagramLabel, problemsLabel = "⌕", "◇", "!"
	}
	btn(inspectLabel, "Inspector", func() {
		a.mu.Lock()
		a.rightTab = "inspector"
		a.renderRightTabs()
		a.renderRight()
		a.mu.Unlock()
	})
	btn(diagramLabel, "Diagrams", func() {
		a.mu.Lock()
		a.rightTab = "diagrams"
		a.ensureDiagramWidthLocked()
		a.loadDiagramLocked(true)
		a.renderRightTabs()
		a.renderRight()
		a.mu.Unlock()
	})
	btn(problemsLabel, "Problems", func() { a.setRightTab("problems") })
	a.addPaneControlsLocked(paneIdx, pane)
}

func (a *app) addPaneControlsLocked(paneIdx int, pane *pane) {
	if a.detached {
		return
	}
	if len(a.panes) > 1 {
		label, title := "Split", "Split editor"
		if a.split {
			label, title = "Unsplit", "Close the second editor pane"
			if len(a.panes) > 1 {
				label = "◫"
			}
		}
		if a.split {
			b := el("button", "tool-btn")
			setText(b, label)
			b.Set("title", title)
			on(b, "click", func(js.Value) { go a.toggleSplit() })
			add(pane.toolbarRight, b)
		} else {
			b := el("button", "tool-btn")
			setText(b, "Split")
			b.Set("title", title)
			on(b, "click", func(js.Value) { go a.toggleSplit() })
			add(pane.toolbarRight, b)
		}
	}
	if a.info.Detachable && pane.tabs[pane.active] != nil {
		b := el("button", "tool-btn")
		label := "Detach"
		if a.split {
			label = "⧉"
		}
		setText(b, label)
		b.Set("title", "Open this file in a separate native window")
		on(b, "click", func(js.Value) { go a.detachPane(paneIdx) })
		add(pane.toolbarRight, b)
	}
}

func (a *app) serverForLanguage(lang string) *Server {
	key := serverKeyForLang(lang)
	if key == "" {
		return nil
	}
	for i := range a.servers {
		if a.servers[i].Key == key {
			return &a.servers[i]
		}
	}
	return nil
}

func serverKeyForLang(lang string) string {
	switch lang {
	case "javascript", "javascriptreact", "typescript", "typescriptreact":
		return "typescript"
	case "css", "scss", "less":
		return "css"
	case "json", "jsonc":
		return "json"
	case "c", "cpp":
		return "c"
	case "go", "python", "html", "rust", "java", "yaml", "markdown", "lua", "shell":
		return lang
	default:
		return ""
	}
}

func (a *app) renderEditorLocked(paneIdx int) {
	pane := a.paneAt(paneIdx)
	if pane == nil || !pane.code.Truthy() {
		return
	}
	clear(pane.code)
	pane.code.Set("style", "")
	t := pane.tabs[pane.active]
	if t == nil {
		if a.workspaceEmpty && paneIdx == 0 {
			a.renderEmptyWorkspacePaneLocked(pane)
		} else {
			a.renderWelcomePaneLocked(pane)
		}
		return
	}
	if t.Loading {
		a.renderEditorMessageLocked(pane, "Loading "+t.Path+"…", "")
		return
	}
	if t.Err != "" {
		a.renderEditorMessageLocked(pane, "Cannot display "+t.Path, t.Err)
		return
	}
	if len(t.LinesText) == 0 {
		a.renderEditorMessageLocked(pane, "Empty file", t.Path+" has no content.")
		return
	}
	lines := t.LinesText
	limit := a.limit
	if limit <= 0 || limit > len(lines) {
		limit = len(lines)
	}
	type diagInfo struct {
		sev  int
		msgs []string
	}
	diagByLine := make(map[int]*diagInfo)
	if fd, ok := a.diags[t.Path]; ok {
		for _, d := range fd.Diagnostics {
			ln := d.Range.Start.Line
			info := diagByLine[ln]
			if info == nil {
				info = &diagInfo{sev: 9}
				diagByLine[ln] = info
			}
			if d.Severity < info.sev {
				info.sev = d.Severity
			}
			info.msgs = append(info.msgs, d.Message)
		}
	}
	funcsAt := make(map[int][]Function)
	for _, fn := range t.Analysis.Functions {
		funcsAt[fn.StartLine-1] = append(funcsAt[fn.StartLine-1], fn)
	}
	tokensByLine := make([][]highlight.Token, len(lines))
	for _, tok := range t.Tokens {
		if tok.Line >= 0 && tok.Line < len(tokensByLine) {
			tokensByLine[tok.Line] = append(tokensByLine[tok.Line], tok)
		}
	}
	for i := 0; i < limit; i++ {
		line := lines[i]
		row := el("div", "code-line")
		setData(row, "line", strconv.Itoa(i))
		classes := "code-line"
		if i == t.Line {
			classes += " active"
		}
		if d := diagByLine[i]; d != nil {
			if d.sev == 1 {
				classes += " diag-err"
			} else if d.sev == 2 {
				classes += " diag-warn"
			}
		}
		switch t.Highlight[i] {
		case "add":
			classes += " diff-add-line"
		case "del":
			classes += " diff-del-line"
		case "search":
			classes += " search-hit-line"
		}
		row.Set("className", classes)
		no := el("span", "line-no")
		setText(no, strconv.Itoa(i+1))
		if d := diagByLine[i]; d != nil {
			no.Set("title", strings.Join(d.msgs, "\n"))
		}
		codeSpan := el("span", "line-code")
		renderTokens(codeSpan, line, tokensByLine[i])
		add(row, no, codeSpan)
		if fns := funcsAt[i]; len(fns) > 0 {
			badges := el("span", "line-badges")
			for _, fn := range fns {
				fnCopy := fn
				bo := el("span", "bigo-inline")
				appendBigO(bo, fn.BigO)
				bo.Set("title", "Static estimate · "+bigOPlainUI(strings.Join(fn.Evidence, " · ")))
				on(bo, "click", func(ev js.Value) {
					ev.Call("stopPropagation")
					go a.selectFunctionInPane(paneIdx, fnCopy)
				})
				cc := el("span", "cc-inline")
				setText(cc, fmt.Sprintf("CC %d", fn.Complexity))
				if fn.Complexity >= 15 {
					cc.Set("className", "cc-inline hot")
				}
				cc.Set("title", "Cyclomatic complexity")
				on(cc, "click", func(ev js.Value) {
					ev.Call("stopPropagation")
					go a.selectFunctionInPane(paneIdx, fnCopy)
				})
				add(badges, bo, cc)
			}
			add(row, badges)
		}
		add(pane.code, row)
	}
	if limit < len(lines) {
		note := el("div", "empty-note")
		setText(note, fmt.Sprintf("Showing the first %d of %d lines. Open the file in an external viewer for the remainder.", limit, len(lines)))
		add(pane.code, note)
	}
	pane.scroll.Set("scrollTop", t.Scroll)
}

func (a *app) renderEditorMessageLocked(pane *pane, title, detail string) {
	w := el("div", "welcome")
	h := el("h1", "")
	setText(h, title)
	p := el("p", "")
	setText(p, detail)
	add(w, h, p)
	pane.code.Set("style", "padding:0;display:flex;min-height:100%")
	add(pane.code, w)
}

func (a *app) renderWelcomePaneLocked(pane *pane) {
	clear(pane.code)
	w := el("div", "welcome")
	if pane.idx > 0 {
		h := el("h1", "")
		setText(h, "Empty editor pane")
		p := el("p", "")
		setText(p, "Select a file in Explorer, or activate a tab. This pane is independent of the other editor.")
		add(w, h, p)
		pane.code.Set("style", "padding:0;display:flex;min-height:100%")
		add(pane.code, w)
		return
	}
	h := el("h1", "")
	setText(h, "Trace the code an agent just wrote.")
	p := el("p", "")
	setText(p, "VibeDE is a read-only lens over your workspace: language-server diagnostics, definitions and references, structure with Big-O estimates, and deterministic diagrams. Nothing here can modify a file.")
	keys := el("p", "")
	keys.Set("innerHTML", `Select a file from the explorer. Use <span class="keys">Ctrl/⌘ + click</span> for a definition and <span class="keys">Alt + click</span> for references.`)
	grid := el("div", "welcome-grid")
	items := [][2]string{
		{"Live agent edits", "Every write is decoded into line diffs and streamed to Activity."},
		{"Flow, not guesswork", "Language servers for Go, Python, TS/JS, HTML/CSS, C/C++ and Rust."},
		{"Cost at a glance", "Per-function Big-O and cyclomatic complexity with evidence lines."},
		{"Split and detach", "Work in two editor panes, resize panels, or detach a file."},
	}
	for _, it := range items {
		card := el("div", "welcome-card")
		b := el("b", "")
		setText(b, it[0])
		s := el("span", "")
		setText(s, it[1])
		add(card, b, s)
		add(grid, card)
	}
	add(w, h, p, keys, grid)
	pane.code.Set("style", "padding:0;display:flex;min-height:100%")
	add(pane.code, w)
}

func (a *app) renderEmptyWorkspacePaneLocked(pane *pane) {
	clear(pane.code)
	w := el("div", "welcome")
	h := el("h1", "")
	setText(h, "This workspace is empty")
	p := el("p", "")
	setText(p, a.root+" contains no readable files.")
	p2 := el("p", "")
	p2.Set("innerHTML", `Use the <b>Open Folder…</b> button in the native window bar to choose the project root.`)
	card := el("div", "welcome-card")
	b := el("b", "")
	setText(b, "Selected root")
	s := el("span", "")
	setText(s, a.root)
	add(card, b, s)
	add(w, h, p, p2, card)
	pane.code.Set("style", "padding:0;display:flex;min-height:100%")
	add(pane.code, w)
}

// Compatibility wrappers used during shell construction and boot.
func (a *app) renderWelcome() {
	a.mu.Lock()
	if p := a.focusedPane(); p != nil {
		a.renderWelcomePaneLocked(p)
	}
	a.mu.Unlock()
}

func (a *app) renderEmptyWorkspace(root string) {
	a.mu.Lock()
	a.workspaceEmpty = true
	if p := a.paneAt(0); p != nil {
		a.renderEmptyWorkspacePaneLocked(p)
	}
	a.mu.Unlock()
}

func renderTokens(parent js.Value, line string, toks []highlight.Token) {
	if len(toks) == 0 {
		add(parent, text(line))
		return
	}
	sortTokens(toks)
	pos := 0
	for _, tok := range toks {
		start, end := tok.Start, tok.End
		if start < pos {
			start = pos
		}
		if start > len(line) {
			break
		}
		if end > len(line) {
			end = len(line)
		}
		if start > pos {
			add(parent, text(line[pos:start]))
		}
		if end > start {
			sp := el("span", "tok-"+tok.Class)
			setText(sp, line[start:end])
			add(parent, sp)
			pos = end
		}
	}
	if pos < len(line) {
		add(parent, text(line[pos:]))
	}
}

func sortTokens(toks []highlight.Token) {
	for i := 1; i < len(toks); i++ {
		for j := i; j > 0 && toks[j-1].Start > toks[j].Start; j-- {
			toks[j-1], toks[j] = toks[j], toks[j-1]
		}
	}
}

func (a *app) onCodeClick(paneIdx int, ev js.Value) {
	target := ev.Get("target")
	if target.Get("closest").Truthy() {
		if badge := target.Call("closest", ".bigo-inline"); badge.Truthy() {
			return
		}
		if badge := target.Call("closest", ".cc-inline"); badge.Truthy() {
			return
		}
	}
	lineEl := target.Call("closest", ".code-line")
	if !lineEl.Truthy() {
		return
	}
	idx, err := strconv.Atoi(getData(lineEl, "line"))
	if err != nil {
		return
	}
	ctrl := ev.Get("ctrlKey").Bool() || ev.Get("metaKey").Bool()
	alt := ev.Get("altKey").Bool()
	a.mu.Lock()
	pane := a.paneAt(paneIdx)
	if pane == nil {
		a.mu.Unlock()
		return
	}
	a.setActivePaneLocked(paneIdx)
	t := pane.tabs[pane.active]
	if t == nil {
		a.mu.Unlock()
		return
	}
	if !ctrl && !alt {
		t.Line = idx
		t.Char = 0
		a.setActiveLineLocked(paneIdx, idx)
		a.renderStatus()
		a.mu.Unlock()
		return
	}
	if idx >= len(t.LinesText) {
		a.mu.Unlock()
		return
	}
	codeEl := lineEl.Call("querySelector", ".line-code")
	rect := codeEl.Call("getBoundingClientRect")
	x := ev.Get("clientX").Float() - rect.Get("left").Float()
	col := columnForX(t.LinesText[idx], x, a.charW)
	method := "definition"
	if alt {
		method = "references"
	}
	t.Line = idx
	t.Char = col
	a.setActiveLineLocked(paneIdx, idx)
	a.renderStatus()
	a.mu.Unlock()
	ev.Call("preventDefault")
	go a.navigateAt(paneIdx, t.Path, idx, col, method)
}

func (a *app) setActiveLineLocked(paneIdx, idx int) {
	pane := a.paneAt(paneIdx)
	if pane == nil {
		return
	}
	lines := pane.code.Call("querySelectorAll", ".code-line.active")
	for i := 0; i < lines.Length(); i++ {
		node := lines.Index(i)
		c := node.Get("className").String()
		c = strings.Replace(c, " active", "", 1)
		node.Set("className", c)
	}
	target := pane.code.Call("querySelector", ".code-line[data-line=\""+strconv.Itoa(idx)+"\"]")
	if target.Truthy() {
		c := target.Get("className").String()
		if !strings.Contains(c, "active") {
			target.Set("className", c+" active")
		}
	}
}

func columnForX(line string, x, charW float64) int {
	if charW <= 0 {
		charW = 7.5
	}
	target := x / charW
	if target < 0 {
		target = 0
	}
	disp := 0.0
	units := 0
	for _, r := range line {
		w := 1.0
		if r == '\t' {
			w = 4
		}
		if disp+w > target+0.4 {
			break
		}
		disp += w
		if r > 0xFFFF {
			units += len(utf16.Encode([]rune{r}))
		} else {
			units++
		}
	}
	return units
}

func (a *app) scrollToLineLocked(paneIdx, line int) {
	pane := a.paneAt(paneIdx)
	if pane == nil {
		return
	}
	if line < 0 {
		line = 0
	}
	top := line*20 - 90
	if top < 0 {
		top = 0
	}
	pane.scroll.Set("scrollTop", top)
	if t := pane.tabs[pane.active]; t != nil {
		t.Scroll = top
	}
	target := pane.code.Call("querySelector", ".code-line[data-line=\""+strconv.Itoa(line)+"\"]")
	if target.Truthy() {
		c := target.Get("className").String()
		target.Set("className", c+" flash")
		fn := js.FuncOf(func(_ js.Value, _ []js.Value) any {
			if target.Truthy() {
				cc := target.Get("className").String()
				cc = strings.Replace(cc, " flash", "", 1)
				target.Set("className", cc)
			}
			return nil
		})
		retained = append(retained, fn)
		js.Global().Call("setTimeout", fn, 1800)
	}
}

func (a *app) navigateAt(paneIdx int, path string, line, char int, method string) {
	locs, err := navigate(path, line, char, method)
	if err != nil {
		a.toast(navErrorText(method, err), "err")
		return
	}
	if len(locs) == 0 {
		a.toast(navEmptyText(method), "warn")
		return
	}
	if method == "references" {
		a.mu.Lock()
		a.activePane = paneIdx
		a.applyPaneFocusLocked()
		a.references = locs
		a.refsFor = fmt.Sprintf("%s:%d", path, line+1)
		a.rightTab = "inspector"
		a.renderRightTabs()
		a.renderRight()
		a.mu.Unlock()
		a.toast(plural(len(locs), "reference", "references")+" found", "")
		return
	}
	first := locs[0]
	if len(locs) > 1 {
		a.toast(fmt.Sprintf("%s returned %d locations; showing the first", method, len(locs)), "")
	}
	go a.openFileInPane(paneIdx, first.Path, first.StartLine, first.StartChar, nil)
}

func navErrorText(method string, err error) string {
	base := "Navigation failed: " + apiErrorHint(err)
	if install := apiErrorInstall(err); install != "" {
		base += " · install: " + install
	}
	return base
}

func navEmptyText(method string) string {
	switch method {
	case "references":
		return "No references reported by the language server."
	case "implementation":
		return "No implementation reported."
	case "typeDefinition":
		return "No type definition reported."
	default:
		return "No definition reported at that position."
	}
}

func (a *app) selectFunction(fn Function) {
	a.selectFunctionInPane(a.activePane, fn)
}

func (a *app) selectFunctionInPane(paneIdx int, fn Function) {
	a.mu.Lock()
	pane := a.paneAt(paneIdx)
	if pane == nil {
		a.mu.Unlock()
		return
	}
	a.activePane = paneIdx
	a.applyPaneFocusLocked()
	fnCopy := fn
	a.selectedFn = &fnCopy
	a.selectedType = nil
	a.rightTab = "inspector"
	a.references = nil
	a.renderRightTabs()
	a.renderRight()
	line := fn.StartLine - 1
	if t := pane.tabs[pane.active]; t != nil {
		t.Line = line
		a.setActiveLineLocked(paneIdx, line)
		a.scrollToLineLocked(paneIdx, line)
		a.renderStatus()
	}
	a.mu.Unlock()
}

func (a *app) selectType(t Type) {
	a.mu.Lock()
	if pane := a.focusedPane(); pane != nil {
		tCopy := t
		a.selectedType = &tCopy
		a.selectedFn = nil
		a.rightTab = "inspector"
		a.renderRightTabs()
		a.renderRight()
		line := t.StartLine - 1
		if pane.tabs[pane.active] != nil {
			a.setActiveLineLocked(a.activePane, line)
			a.scrollToLineLocked(a.activePane, line)
		}
	}
	a.mu.Unlock()
}

func (a *app) onDiagnostics(fd FileDiagnostics) {
	a.mu.Lock()
	if len(fd.Diagnostics) == 0 {
		delete(a.diags, fd.Path)
	} else {
		a.diags[fd.Path] = fd
	}
	for _, pane := range a.panes {
		t := pane.tabs[fd.Path]
		if t == nil {
			continue
		}
		t.Diags = fd
		a.renderTabsLocked(pane.idx)
		if pane.active == fd.Path && a.paneVisible(pane.idx) {
			t.Scroll = pane.scroll.Get("scrollTop").Int()
			a.renderEditorLocked(pane.idx)
			a.renderToolbarLocked(pane.idx)
		}
	}
	if a.activePath() == fd.Path {
		a.renderStatus()
	}
	a.renderRightTabs()
	if a.rightTab == "problems" {
		a.renderRight()
	}
	if a.rightTab == "inspector" && a.selectedFn == nil && a.selectedType == nil {
		a.renderRight()
	}
	a.mu.Unlock()
}

func (a *app) onChange(ch Change) {
	key := ch.Path + "|" + ch.Kind + "|" + ch.At.String()
	a.mu.Lock()
	if a.seen[key] {
		a.mu.Unlock()
		return
	}
	a.seen[key] = true
	a.changes = append([]Change{ch}, a.changes...)
	if len(a.changes) > 200 {
		a.changes = a.changes[:200]
	}
	a.refreshChipsLocked()
	a.renderRightTabs()
	if a.rightTab == "activity" {
		a.renderRight()
	}
	a.renderStatus()
	open := a.isOpen(ch.Path)
	a.mu.Unlock()

	if !ch.IsDir && open && (ch.Kind == "modified" || ch.Kind == "created" || ch.Kind == "deleted") {
		go func() {
			time.Sleep(160 * time.Millisecond)
			a.refreshTab(ch.Path)
		}()
	}
	if ch.Kind == "created" || ch.Kind == "deleted" || ch.IsDir || ch.Kind == "directory" {
		go a.refreshTreeFor(ch.Path)
	}
}

func (a *app) onServer(s Server) {
	a.mu.Lock()
	found := false
	for i := range a.servers {
		if a.servers[i].Key == s.Key {
			a.servers[i] = s
			found = true
			break
		}
	}
	if !found {
		a.servers = append(a.servers, s)
	}
	a.renderServersLocked()
	for _, p := range a.panes {
		a.renderToolbarLocked(p.idx)
	}
	a.renderStatus()
	a.mu.Unlock()
}

func (a *app) applyAllDiagnostics() {
	files, err := fetchAllDiagnostics()
	if err != nil {
		return
	}
	a.mu.Lock()
	for _, fd := range files {
		if len(fd.Diagnostics) > 0 {
			a.diags[fd.Path] = fd
		}
	}
	a.renderTreeLocked("")
	for _, p := range a.panes {
		a.renderTabsLocked(p.idx)
		if p.active != "" {
			a.renderEditorLocked(p.idx)
		}
	}
	a.renderRightTabs()
	a.renderRight()
	a.renderStatus()
	a.mu.Unlock()
}

func (a *app) updateTreeActiveLocked() {
	active := a.focusedPath()
	rows := a.els.tree.Call("querySelectorAll", ".tree-row")
	for i := 0; i < rows.Length(); i++ {
		row := rows.Index(i)
		title := ""
		tv := row.Get("title")
		if tv.Truthy() {
			title = tv.String()
		}
		cls := row.Get("className").String()
		if title == active && active != "" {
			if !strings.Contains(cls, "active") {
				row.Set("className", cls+" active")
			}
			row.Call("scrollIntoView", map[string]any{"block": "nearest"})
		} else {
			cls = strings.Replace(cls, " active", "", 1)
			row.Set("className", cls)
		}
	}
}

func (a *app) activePath() string { return a.focusedPath() }

// openChange jumps from an Activity item to the first changed line and marks
// the added/deleted lines in the editor.
func (a *app) openChange(ch Change) {
	if ch.IsDir {
		a.toast("Directory changes do not have a text location.", "warn")
		return
	}
	if ch.Kind == "deleted" {
		a.toast("This file was deleted; there is no current line to open.", "warn")
		return
	}
	if ch.Binary {
		a.toast("Binary files cannot be shown in the code viewer.", "warn")
		return
	}
	target, marks := diffHighlight(ch)
	a.openFileInPane(a.activePane, ch.Path, target, 0, marks)
	if len(marks) > 0 {
		a.toast(fmt.Sprintf("Jumped to changed line %d", target+1), "")
	}
}

func diffHighlight(ch Change) (int, map[int]string) {
	target := -1
	fallback := -1
	marks := make(map[int]string)
	for _, h := range ch.Hunks {
		if fallback < 0 && h.NewStart > 0 {
			fallback = h.NewStart - 1
		}
		for _, ln := range h.Lines {
			switch ln.Kind {
			case "+":
				if ln.NewNo > 0 {
					marks[ln.NewNo-1] = "add"
					if target < 0 {
						target = ln.NewNo - 1
					}
				}
			case "-":
				idx := h.NewStart - 1
				if idx < 0 {
					idx = 0
				}
				marks[idx] = "del"
				if target < 0 {
					target = idx
				}
			}
		}
	}
	if target < 0 {
		target = fallback
	}
	if target < 0 {
		target = 0
	}
	return target, marks
}
