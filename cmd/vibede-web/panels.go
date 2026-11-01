//go:build js && wasm

package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"syscall/js"
	"time"
)

func (a *app) setRightTab(tab string) {
	a.mu.Lock()
	a.rightTab = tab
	if tab == "diagrams" {
		a.ensureDiagramWidthLocked()
	}
	a.renderRightTabs()
	if tab == "diagrams" {
		a.loadDiagramLocked(true)
	}
	a.renderRight()
	a.mu.Unlock()
	if tab == "search" {
		a.focusCodeSearch()
	}
}

func (a *app) renderRightTabs() {
	if !a.els.rightTabs.Truthy() {
		return
	}
	clear(a.els.rightTabs)
	a.applyWorkspaceClassLocked()
	problems := 0
	for _, fd := range a.diags {
		problems += fd.Counts.Errors + fd.Counts.Warnings
	}
	tabs := []struct {
		key   string
		label string
		count int
	}{
		{"activity", "Activity", len(a.changes) + len(a.explanations)},
		{"problems", "Problems", problems},
		{"search", "Search", len(a.searchResults)},
		{"diagrams", "Diagrams", 0},
		{"inspector", "Inspector", 0},
	}
	for _, t := range tabs {
		btn := el("button", "rtab")
		if a.rightTab == t.key {
			btn.Set("className", "rtab active")
		}
		label := el("span", "")
		setText(label, t.label)
		add(btn, label)
		if t.count > 0 {
			c := el("span", "count")
			setText(c, strconv.Itoa(t.count))
			add(btn, c)
		}
		key := t.key
		on(btn, "click", func(js.Value) { a.setRightTab(key) })
		add(a.els.rightTabs, btn)
	}
}

func (a *app) renderRight() {
	if !a.els.rightBody.Truthy() {
		return
	}
	switch a.rightTab {
	case "problems":
		a.renderProblemsLocked()
	case "search":
		a.renderSearchLocked()
	case "diagrams":
		a.renderDiagramsLocked()
	case "inspector":
		a.renderInspectorLocked()
	default:
		a.renderActivityLocked()
	}
}

func (a *app) renderActivityLocked() {
	clear(a.els.rightBody)
	panel := el("div", "panel")
	if len(a.changes) == 0 && len(a.explanations) == 0 {
		note := el("div", "panel-empty")
		setText(note, "No edits or explanations yet. Agent writes appear here in real time; selections you ask about are added with their answers.")
		add(panel, note)
		a.addArchiveNote(panel)
		add(a.els.rightBody, panel)
		return
	}
	type entry struct {
		at time.Time
		ch *Change
		ex *ExplainRecord
	}
	items := make([]entry, 0, len(a.changes)+len(a.explanations))
	for i := range a.changes {
		items = append(items, entry{at: a.changes[i].At, ch: &a.changes[i]})
	}
	for i := range a.explanations {
		items = append(items, entry{at: a.explanations[i].At, ex: &a.explanations[i]})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at.After(items[j].at) })
	if len(items) > 300 {
		items = items[:300]
	}
	list := el("div", "activity-list")
	for _, it := range items {
		if it.ex != nil {
			add(list, a.explanationItem(*it.ex))
			continue
		}
		add(list, a.changeItem(*it.ch))
	}
	add(panel, list)
	a.addArchiveNote(panel)
	add(a.els.rightBody, panel)
}

func (a *app) changeItem(ch Change) js.Value {
	item := el("div", "activity-item")
	head := el("div", "activity-head")
	kind := el("span", "activity-kind "+ch.Kind)
	label := ch.Kind
	if ch.IsDir {
		label = "dir"
	}
	setText(kind, label)
	path := el("span", "activity-path")
	setText(path, ch.Path)
	when := el("span", "activity-time")
	setText(when, changeTime(ch.At))
	add(head, kind, path, when)
	add(item, head)
	if ch.Binary {
		note := el("div", "activity-stats")
		setText(note, "binary file changed")
		add(item, note)
	} else if ch.Stats.Added+ch.Stats.Removed > 0 || ch.HunksOmitted {
		stats := el("div", "activity-stats")
		addEl := el("span", "stat-add")
		setText(addEl, "+"+strconv.Itoa(ch.Stats.Added))
		del := el("span", "stat-del")
		setText(del, "−"+strconv.Itoa(ch.Stats.Removed))
		add(stats, addEl, del)
		if ch.HunksOmitted {
			om := el("span", "")
			setText(om, "diff truncated")
			add(stats, om)
		}
		add(item, stats)
		if len(ch.Hunks) > 0 {
			add(item, renderDiff(ch.Hunks, 14))
		}
	}
	c := ch
	on(item, "click", func(js.Value) {
		go a.openChange(c)
	})
	return item
}

func (a *app) explanationItem(ex ExplainRecord) js.Value {
	item := el("div", "activity-item explain-item")
	head := el("div", "activity-head")
	kind := el("span", "activity-kind explain")
	setText(kind, "explain")
	label := ex.Path
	if label == "" {
		label = "selection"
	}
	if ex.StartLine > 0 {
		if ex.StartLine == ex.EndLine {
			label = fmt.Sprintf("%s:%d", label, ex.StartLine)
		} else {
			label = fmt.Sprintf("%s:%d-%d", label, ex.StartLine, ex.EndLine)
		}
	}
	path := el("span", "activity-path")
	setText(path, label)
	path.Set("title", label)
	when := el("span", "activity-time")
	setText(when, changeTime(ex.At))
	add(head, kind, path, when)
	add(item, head)

	q := el("div", "explain-q")
	setText(q, "Q: "+ex.Question)
	add(item, q)
	answer := el("pre", "activity-answer")
	setText(answer, ex.Answer)
	add(item, answer)

	meta := el("div", "activity-meta")
	parts := []string{ex.Model}
	if len(ex.Context) > 0 {
		noun := "files"
		if len(ex.Context) == 1 {
			noun = "file"
		}
		parts = append(parts, fmt.Sprintf("%d %s: %s", len(ex.Context), noun, strings.Join(ex.Context, ", ")))
	}
	if len(ex.Annotations) > 0 {
		noun := "line notes"
		if len(ex.Annotations) == 1 {
			noun = "line note"
		}
		parts = append(parts, fmt.Sprintf("%d %s · click to show", len(ex.Annotations), noun))
	}
	setText(meta, strings.Join(parts, " · "))
	add(item, meta)

	if ex.Path != "" {
		e := ex
		on(item, "click", func(js.Value) {
			go a.applyAnnotations(e)
		})
	}
	return item
}

// addArchiveNote shows where the history lives and how much it holds.
func (a *app) addArchiveNote(panel js.Value) {
	path := a.archive.Path
	if path == "" {
		path = ".vibede.rab"
	}
	note := el("div", "archive-note")
	exNoun, edNoun := "explanations", "edits"
	if len(a.explanations) == 1 {
		exNoun = "explanation"
	}
	if len(a.changes) == 1 {
		edNoun = "edit"
	}
	text := fmt.Sprintf("%s · %d %s · %d %s", path, len(a.explanations), exNoun, len(a.changes), edNoun)
	if a.archive.Bytes > 0 {
		text += " · " + compactNum(a.archive.Bytes)
	}
	setText(note, text)
	note.Set("title", "History is stored gzip-compressed in the workspace root. Explanation contexts are not stored, only file names.")
	add(panel, note)
}

func changeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("15:04:05")
}

func renderDiff(hunks []Hunk, maxLines int) js.Value {
	box := el("div", "diff-lines")
	shown := 0
	for _, h := range hunks {
		for _, ln := range h.Lines {
			if shown >= maxLines {
				more := el("div", "diff-line diff-ctx")
				setText(more, "…")
				add(box, more)
				return box
			}
			row := el("div", "diff-line")
			no := el("span", "diff-no")
			switch ln.Kind {
			case "+":
				row.Set("className", "diff-line diff-add")
				setText(no, strconv.Itoa(ln.NewNo))
			case "-":
				row.Set("className", "diff-line diff-del")
				setText(no, strconv.Itoa(ln.OldNo))
			default:
				row.Set("className", "diff-line diff-ctx")
				setText(no, strconv.Itoa(ln.NewNo))
			}
			code := el("span", "")
			setText(code, ln.Kind+" "+ln.Text)
			add(row, no, code)
			add(box, row)
			shown++
		}
	}
	return box
}

func (a *app) renderProblemsLocked() {
	clear(a.els.rightBody)
	panel := el("div", "panel")
	total := 0
	for _, fd := range a.diags {
		total += len(fd.Diagnostics)
	}
	if total == 0 {
		note := el("div", "panel-empty")
		setText(note, "No diagnostics reported. Open a file and give its language server a moment; missing servers are listed in the sidebar with install hints.")
		add(panel, note)
		add(a.els.rightBody, panel)
		return
	}
	paths := make([]string, 0, len(a.diags))
	for p, fd := range a.diags {
		if len(fd.Diagnostics) > 0 {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		fd := a.diags[p]
		group := el("div", "problem-group")
		file := el("div", "problem-file")
		setText(file, p)
		ff := p
		on(file, "click", func(js.Value) { go a.openFile(ff, 0, 0, false) })
		add(group, file)
		for _, d := range fd.Diagnostics {
			item := el("div", "problem-item")
			sev := el("span", "sev "+sevClass(d.Severity))
			setText(sev, sevGlyph(d.Severity))
			body := el("div", "problem-body")
			msg := el("div", "problem-msg")
			setText(msg, d.Message)
			meta := el("div", "problem-meta")
			extra := ""
			if d.Source != "" {
				extra = " · " + d.Source
			}
			setText(meta, fmt.Sprintf("%d:%d%s", d.Range.Start.Line+1, d.Range.Start.Character+1, extra))
			add(body, msg, meta)
			add(item, sev, body)
			loc := d
			path := p
			on(item, "click", func(js.Value) {
				go a.openFile(path, loc.Range.Start.Line, loc.Range.Start.Character, true)
			})
			add(group, item)
		}
		add(panel, group)
	}
	add(a.els.rightBody, panel)
}

// ---- outline ----

func (a *app) renderOutlineLocked() {
	if !a.els.outline.Truthy() {
		return
	}
	clear(a.els.outline)
	t := a.activeTab()
	if t == nil {
		note := el("div", "empty-note")
		setText(note, "Open a file to see functions and types.")
		add(a.els.outline, note)
		return
	}
	if t.Loading {
		note := el("div", "empty-note")
		setText(note, "Analyzing…")
		add(a.els.outline, note)
		return
	}
	type item struct {
		line int
		kind string
		fn   *Function
		ty   *Type
		name string
	}
	var items []item
	for i := range t.Analysis.HTML {
		h := &t.Analysis.HTML[i]
		label := "<" + h.Tag + ">"
		if h.ID != "" {
			label = "<" + h.Tag + "#" + h.ID + ">"
		}
		items = append(items, item{line: h.Line, kind: "el", name: strings.Repeat("  ", clampInt(h.Depth, 0, 10)) + label})
	}
	for i := range t.Analysis.Rules {
		r := &t.Analysis.Rules[i]
		items = append(items, item{line: r.Line, kind: "css", name: r.Selector})
	}
	for i := range t.Analysis.Types {
		ty := &t.Analysis.Types[i]
		items = append(items, item{line: ty.StartLine, kind: "ty", ty: ty, name: ty.Name})
	}
	for i := range t.Analysis.Functions {
		fn := &t.Analysis.Functions[i]
		name := fn.Name
		if fn.Receiver != "" {
			name = fn.Receiver + "." + fn.Name
		}
		items = append(items, item{line: fn.StartLine, kind: "fn", fn: fn, name: name})
	}
	if len(items) == 0 {
		note := el("div", "empty-note")
		setText(note, "No functions or types detected.")
		add(a.els.outline, note)
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].line != items[j].line {
			return items[i].line < items[j].line
		}
		return items[i].name < items[j].name
	})
	for _, it := range items {
		row := el("div", "outline-item")
		kind := el("span", "outline-kind "+it.kind)
		switch it.kind {
		case "fn":
			setText(kind, "ƒ")
		case "ty":
			setText(kind, "T")
		case "el":
			setText(kind, "<>")
		case "css":
			setText(kind, "{}")
		}
		name := el("span", "outline-name")
		setText(name, it.name)
		meta := el("span", "outline-meta")
		if it.fn != nil {
			bo := el("span", "badge bigO")
			appendBigO(bo, it.fn.BigO)
			cc := el("span", "badge cc")
			setText(cc, "CC "+strconv.Itoa(it.fn.Complexity))
			if it.fn.Complexity >= 15 {
				cc.Set("className", "badge cc hot")
			}
			add(meta, bo, cc)
		} else if it.ty != nil {
			tb := el("span", "badge type")
			setText(tb, it.ty.Kind)
			add(meta, tb)
		}
		add(row, kind, name, meta)
		row.Set("title", fmt.Sprintf("line %d", it.line))
		itCopy := it
		on(row, "click", func(js.Value) {
			if itCopy.fn != nil {
				go a.selectFunction(*itCopy.fn)
			} else if itCopy.ty != nil {
				go a.selectType(*itCopy.ty)
			} else {
				a.mu.Lock()
				if cur := a.activeTab(); cur != nil {
					cur.Line = itCopy.line - 1
					a.setActiveLineLocked(a.activePane, cur.Line)
					a.scrollToLineLocked(a.activePane, cur.Line)
					a.renderStatus()
				}
				a.mu.Unlock()
			}
		})
		add(a.els.outline, row)
	}
}

// ---- servers ----

func (a *app) renderServersLocked() {
	if !a.els.servers.Truthy() {
		return
	}
	clear(a.els.servers)
	if len(a.servers) == 0 {
		note := el("div", "empty-note")
		setText(note, "No language servers configured.")
		add(a.els.servers, note)
		return
	}
	for i := range a.servers {
		s := a.servers[i]
		row := el("div", "server-row")
		top := el("div", "server-top")
		dot := el("span", "dot")
		switch s.Status {
		case "running":
			dot.Set("className", "dot ok")
		case "starting":
			dot.Set("className", "dot live")
		case "failed", "missing":
			dot.Set("className", "dot err")
		case "available":
			dot.Set("className", "dot live")
		default:
			dot.Set("className", "dot")
		}
		name := el("span", "server-name")
		setText(name, s.Name)
		status := el("span", "server-status "+s.Status)
		setText(status, s.Status)
		add(top, dot, name, status)
		if s.Installed {
			btn := el("button", "mini-btn")
			setText(btn, "↻")
			btn.Set("title", "Restart "+s.Name)
			key := s.Key
			on(btn, "click", func(ev js.Value) {
				ev.Call("stopPropagation")
				go a.restartServer(key)
			})
			add(top, btn)
		}
		add(row, top)
		hint := el("div", "server-hint")
		var lines []string
		if s.Error != "" {
			lines = append(lines, s.Error)
		}
		if s.Install != "" && (s.Status == "missing" || !s.Installed) {
			lines = append(lines, "install: "+s.Install)
		}
		if s.Command != "" {
			lines = append(lines, "cmd: "+s.Command+" "+strings.Join(s.Args, " "))
		}
		if s.OpenDocs > 0 {
			lines = append(lines, fmt.Sprintf("%d open document(s), %d diagnostics", s.OpenDocs, s.Diagnostics))
		}
		setText(hint, strings.Join(lines, "\n"))
		add(row, hint)
		row.Set("title", strings.Join(s.Languages, ", "))
		add(a.els.servers, row)
	}
	a.refreshChipsLocked()
}

func (a *app) restartServer(key string) {
	servers, err := restartServer(key)
	if err != nil {
		a.toast("Restart failed: "+apiErrorHint(err), "err")
		return
	}
	a.mu.Lock()
	a.servers = servers
	a.renderServersLocked()
	for _, p := range a.panes {
		a.renderToolbarLocked(p.idx)
	}
	a.renderStatus()
	a.mu.Unlock()
	a.toast("Server restarted", "")
}

// ---- diagrams ----

func (a *app) loadDiagramLocked(force bool) {
	t := a.activeTab()
	if t == nil {
		a.diagram = nil
		a.diagLoad = false
		a.diagErr = "Open a file to generate a diagram."
		return
	}
	if t.Loading {
		a.diagram = nil
		a.diagLoad = false
		a.diagErr = "Waiting for the file to load…"
		return
	}
	kind := a.diagKind
	if kind == "" {
		kind = "flowchart"
	}
	fn := a.diagFn
	if fn != "" {
		valid := false
		for _, f := range t.Analysis.Functions {
			if fnKey(f) == fn {
				valid = true
				break
			}
		}
		if !valid {
			fn = ""
			a.diagFn = ""
		}
	}
	if !force && a.diagram != nil && a.diagPath == t.Path && a.diagKindApplied == kind && a.diagFnApplied == fn {
		return
	}
	a.diagLoad = true
	a.diagErr = ""
	path := t.Path
	a.diagPath = path
	a.diagKindApplied = kind
	a.diagFnApplied = fn
	a.renderRight()
	go func() {
		doc, err := fetchDiagram(path, kind, fn)
		a.mu.Lock()
		if a.diagPath != path || a.diagKindApplied != kind || a.diagFnApplied != fn {
			a.mu.Unlock()
			return
		}
		a.diagLoad = false
		if err != nil {
			a.diagram = nil
			a.diagErr = apiErrorHint(err)
			if install := apiErrorInstall(err); install != "" {
				a.diagErr += " · " + install
			}
		} else {
			d := doc
			a.diagram = &d
			a.diagErr = ""
		}
		if a.rightTab == "diagrams" {
			a.renderRight()
		}
		a.mu.Unlock()
	}()
}

func fnKey(fn Function) string {
	if fn.Receiver != "" {
		return fn.Receiver + "." + fn.Name
	}
	return fn.Name
}

func (a *app) renderDiagramsLocked() {
	clear(a.els.rightBody)
	t := a.activeTab()
	controls := el("div", "diagram-controls")
	kindRow := el("div", "kind-buttons")
	kinds := []struct{ key, label string }{
		{"flowchart", "Flowchart"},
		{"class", "UML"},
		{"usecase", "Use cases"},
		{"sequence", "Sequence"},
		{"modules", "Imports"},
	}
	if t != nil && t.File.Language == "html" {
		kinds = append(kinds, struct{ key, label string }{"dom", "DOM"})
	}
	for _, k := range kinds {
		btn := el("button", "tool-btn")
		if a.diagKind == k.key {
			btn.Set("className", "tool-btn active")
		}
		setText(btn, k.label)
		key := k.key
		on(btn, "click", func(js.Value) {
			a.mu.Lock()
			a.diagKind = key
			a.diagFn = ""
			a.loadDiagramLocked(true)
			a.mu.Unlock()
		})
		add(kindRow, btn)
	}
	add(controls, kindRow)
	if t != nil && (a.diagKind == "flowchart" || a.diagKind == "sequence") && len(t.Analysis.Functions) > 0 {
		selectEl := el("select", "select")
		if a.diagKind == "flowchart" {
			opt := el("option", "")
			opt.Set("value", "")
			setText(opt, "Call map · all functions")
			add(selectEl, opt)
		} else {
			opt := el("option", "")
			opt.Set("value", "")
			setText(opt, "Root · main / first")
			add(selectEl, opt)
		}
		for _, fn := range t.Analysis.Functions {
			opt := el("option", "")
			key := fnKey(fn)
			opt.Set("value", key)
			setText(opt, key+"  ·  "+bigOPlainUI(fn.BigO))
			if a.diagFn == key {
				opt.Set("selected", true)
			}
			add(selectEl, opt)
		}
		on(selectEl, "change", func(ev js.Value) {
			val := ev.Get("target").Get("value").String()
			a.mu.Lock()
			a.diagFn = val
			a.loadDiagramLocked(true)
			a.mu.Unlock()
		})
		add(controls, selectEl)
	}
	srcRow := el("div", "kind-buttons")
	for _, m := range []string{"svg", "dot", "mermaid"} {
		btn := el("button", "tool-btn")
		if a.diagMode == m {
			btn.Set("className", "tool-btn active")
		}
		setText(btn, strings.ToUpper(m))
		mode := m
		on(btn, "click", func(js.Value) {
			a.mu.Lock()
			a.diagMode = mode
			a.renderRight()
			a.mu.Unlock()
		})
		add(srcRow, btn)
	}
	reload := el("button", "tool-btn")
	setText(reload, "reload")
	on(reload, "click", func(js.Value) {
		a.mu.Lock()
		a.loadDiagramLocked(true)
		a.mu.Unlock()
	})
	copyBtn := el("button", "tool-btn")
	setText(copyBtn, "copy")
	on(copyBtn, "click", func(js.Value) {
		a.mu.Lock()
		text := ""
		if a.diagram != nil {
			switch a.diagMode {
			case "dot":
				text = a.diagram.DOT
			case "mermaid":
				text = a.diagram.Mermaid
			default:
				text = a.diagram.SVG
			}
		}
		a.mu.Unlock()
		if text != "" {
			copyToClipboard(text)
			a.toast("Copied diagram source", "")
		}
	})
	add(srcRow, reload, copyBtn)
	add(controls, srcRow)

	zoomRow := el("div", "kind-buttons")
	zoomOut := el("button", "tool-btn")
	setText(zoomOut, "−")
	zoomOut.Set("title", "Zoom out")
	on(zoomOut, "click", func(js.Value) {
		a.mu.Lock()
		a.diagZoom -= 0.15
		if a.diagZoom < 0.2 {
			a.diagZoom = 0.2
		}
		a.renderRight()
		a.mu.Unlock()
	})
	zoomIn := el("button", "tool-btn")
	setText(zoomIn, "+")
	zoomIn.Set("title", "Zoom in")
	on(zoomIn, "click", func(js.Value) {
		a.mu.Lock()
		a.diagZoom += 0.15
		if a.diagZoom > 2.5 {
			a.diagZoom = 2.5
		}
		a.renderRight()
		a.mu.Unlock()
	})
	fit := el("button", "tool-btn")
	setText(fit, "Fit")
	fit.Set("title", "Fit the diagram to the panel width")
	on(fit, "click", func(js.Value) {
		a.mu.Lock()
		if a.diagNaturalW > 0 && a.rightWidth > 0 {
			avail := float64(a.rightWidth - 40)
			z := avail / float64(a.diagNaturalW)
			if z < 0.15 {
				z = 0.15
			}
			if z > 1.5 {
				z = 1.5
			}
			a.diagZoom = z
			a.renderRight()
		}
		a.mu.Unlock()
	})
	reset := el("button", "tool-btn")
	setText(reset, "100%")
	reset.Set("title", "Reset diagram zoom")
	on(reset, "click", func(js.Value) {
		a.mu.Lock()
		a.diagZoom = 1
		a.renderRight()
		a.mu.Unlock()
	})
	zoomLabel := el("span", "tool-btn")
	setText(zoomLabel, fmt.Sprintf("%d%%", int(a.diagZoom*100)))
	zoomRow.Set("style", "align-items:center")
	add(zoomRow, zoomOut, zoomLabel, zoomIn, fit, reset)
	add(controls, zoomRow)
	add(a.els.rightBody, controls)

	if t == nil {
		note := el("div", "panel-empty")
		setText(note, "Open a file to generate a diagram.")
		add(a.els.rightBody, note)
		return
	}
	if a.diagLoad {
		note := el("div", "panel-empty")
		setText(note, "Rendering diagram…")
		add(a.els.rightBody, note)
		return
	}
	if a.diagErr != "" {
		note := el("div", "panel-empty")
		setText(note, a.diagErr)
		add(a.els.rightBody, note)
		return
	}
	if a.diagram == nil {
		note := el("div", "panel-empty")
		setText(note, "Choose a diagram kind.")
		add(a.els.rightBody, note)
		return
	}
	doc := a.diagram
	if a.diagMode == "svg" {
		stage := el("div", "diagram-stage")
		stage.Set("innerHTML", doc.SVG)
		svg := stage.Call("querySelector", "svg")
		if svg.Truthy() {
			if w, err := strconv.ParseFloat(svg.Call("getAttribute", "width").String(), 64); err == nil && w > 0 {
				a.diagNaturalW = int(w)
				svg.Get("style").Set("width", fmt.Sprintf("%.1fpx", w*a.diagZoom))
				svg.Get("style").Set("height", "auto")
			}
		}
		add(a.els.rightBody, stage)
	} else {
		src := el("pre", "diagram-src")
		if a.diagMode == "dot" {
			setText(src, doc.DOT)
		} else {
			setText(src, doc.Mermaid)
		}
		add(a.els.rightBody, src)
	}
	meta := el("div", "diagram-meta")
	info := el("span", "")
	setText(info, fmt.Sprintf("%d nodes · %d edges · %d%% scale · deterministic", doc.Nodes, doc.Edges, int(a.diagZoom*100)))
	title := el("span", "")
	setText(title, doc.Title)
	meta.Set("title", doc.Title)
	add(meta, info)
	add(a.els.rightBody, meta)
}

func copyToClipboard(s string) {
	clip := js.Global().Get("navigator").Get("clipboard")
	if clip.IsUndefined() || clip.IsNull() {
		return
	}
	clip.Call("writeText", s)
}

// ---- inspector ----

func (a *app) renderInspectorLocked() {
	clear(a.els.rightBody)
	panel := el("div", "panel")
	if len(a.references) > 0 {
		head := el("div", "section-label")
		setText(head, "References · "+a.refsFor)
		add(panel, head)
		for _, loc := range a.references {
			row := el("div", "func-row")
			name := el("span", "fn-name")
			setText(name, fmt.Sprintf("%s:%d:%d", loc.Path, loc.StartLine+1, loc.StartChar+1))
			add(row, name)
			l := loc
			on(row, "click", func(js.Value) { go a.openFile(l.Path, l.StartLine, l.StartChar, true) })
			add(panel, row)
		}
		add(panel, el("div", "section-label"))
	}
	if a.selectedFn != nil {
		a.renderFunctionInspectorLocked(panel, *a.selectedFn)
		add(a.els.rightBody, panel)
		return
	}
	if a.selectedType != nil {
		a.renderTypeInspectorLocked(panel, *a.selectedType)
		add(a.els.rightBody, panel)
		return
	}
	t := a.activeTab()
	if t == nil {
		note := el("div", "panel-empty")
		setText(note, "Select a function, type or file to inspect.")
		add(panel, note)
		add(a.els.rightBody, panel)
		return
	}
	head := el("div", "inspect-head")
	title := el("div", "inspect-title")
	setText(title, t.Path)
	sub := el("div", "inspect-sub")
	setText(sub, fmt.Sprintf("%s · %d lines · %d SLOC · %s", langName(t.File.Language), t.File.Lines, t.Analysis.SLOC, compactNum(t.File.Size)))
	add(head, title, sub)
	add(panel, head)
	kv := el("div", "kv")
	addKV := func(k, v string) {
		kk := el("div", "k")
		setText(kk, k)
		vv := el("div", "v")
		setText(vv, v)
		add(kv, kk, vv)
	}
	addKVBigO := func(k, v string) {
		kk := el("div", "k")
		setText(kk, k)
		vv := el("div", "v bigo-value")
		appendBigO(vv, v)
		add(kv, kk, vv)
	}
	addKV("functions", strconv.Itoa(len(t.Analysis.Functions)))
	addKV("types", strconv.Itoa(len(t.Analysis.Types)))
	addKVBigO("dominant", t.Analysis.Metrics.DominantBigO)
	addKV("max CC", strconv.Itoa(t.Analysis.Metrics.MaxComplexity))
	addKV("avg CC", strconv.Itoa(t.Analysis.Metrics.AvgComplexity))
	if len(t.Analysis.Imports) > 0 {
		addKV("imports", strconv.Itoa(len(t.Analysis.Imports)))
	}
	add(panel, kv)
	if len(t.Analysis.Functions) > 0 {
		label := el("div", "section-label")
		setText(label, "Functions")
		add(panel, label)
		list := el("div", "func-list")
		for i := range t.Analysis.Functions {
			fn := t.Analysis.Functions[i]
			row := el("div", "func-row")
			name := el("span", "fn-name")
			setText(name, fnKey(fn))
			meta := el("span", "fn-meta")
			bo := el("span", "badge bigO")
			appendBigO(bo, fn.BigO)
			cc := el("span", "badge cc")
			setText(cc, "CC "+strconv.Itoa(fn.Complexity))
			add(meta, bo, cc)
			add(row, name, meta)
			f := fn
			on(row, "click", func(js.Value) { go a.selectFunction(f) })
			add(list, row)
		}
		add(panel, list)
	}
	if len(t.Analysis.Types) > 0 {
		label := el("div", "section-label")
		setText(label, "Types")
		add(panel, label)
		list := el("div", "func-list")
		for i := range t.Analysis.Types {
			ty := t.Analysis.Types[i]
			row := el("div", "func-row")
			name := el("span", "fn-name")
			setText(name, ty.Name)
			meta := el("span", "fn-meta")
			b := el("span", "badge type")
			setText(b, ty.Kind)
			add(meta, b)
			add(row, name, meta)
			tt := ty
			on(row, "click", func(js.Value) { go a.selectType(tt) })
			add(list, row)
		}
		add(panel, list)
	}
	for _, n := range t.Analysis.Notes {
		note := el("div", "empty-note")
		setText(note, n)
		add(panel, note)
	}
	add(a.els.rightBody, panel)
}

func (a *app) renderFunctionInspectorLocked(panel js.Value, fn Function) {
	head := el("div", "inspect-head")
	title := el("div", "inspect-title")
	label := fn.Name
	if fn.Receiver != "" {
		label = fn.Receiver + "." + fn.Name
	}
	setText(title, label)
	sub := el("div", "inspect-sub")
	text := fmt.Sprintf("%s · lines %d–%d", fn.Kind, fn.StartLine, fn.EndLine)
	if fn.Recursive {
		text += " · recursive"
	}
	setText(sub, text)
	add(head, title, sub)
	add(panel, head)
	sig := el("div", "inspect-signature")
	setText(sig, fn.Signature)
	add(panel, sig)
	kv := el("div", "kv")
	addKV := func(k, v string) {
		kk := el("div", "k")
		setText(kk, k)
		vv := el("div", "v")
		setText(vv, v)
		add(kv, kk, vv)
	}
	addKVBigO := func(k, v string) {
		kk := el("div", "k")
		setText(kk, k)
		vv := el("div", "v bigo-value")
		appendBigO(vv, v)
		add(kv, kk, vv)
	}
	addKVBigO("Big-O", fn.BigO)
	addKV("cyclomatic", strconv.Itoa(fn.Complexity))
	if len(fn.Calls) > 0 {
		addKV("calls", strings.Join(fn.Calls, ", "))
	}
	add(panel, kv)
	if len(fn.Evidence) > 0 {
		label := el("div", "section-label")
		setText(label, "Big-O evidence")
		add(panel, label)
		list := el("ul", "evidence")
		for _, e := range fn.Evidence {
			li := el("li", "")
			appendBigO(li, e)
			add(list, li)
		}
		add(panel, list)
	}
	refs := el("button", "tool-btn")
	setText(refs, "Find references")
	on(refs, "click", func(js.Value) {
		go a.navigateAt(a.activePane, a.activePath(), fn.StartLine-1, fn.StartChar, "references")
	})
	diag := el("button", "tool-btn")
	setText(diag, "Control-flow diagram")
	on(diag, "click", func(js.Value) {
		a.mu.Lock()
		a.diagKind = "flowchart"
		a.diagFn = fnKey(fn)
		a.rightTab = "diagrams"
		a.loadDiagramLocked(true)
		a.renderRightTabs()
		a.mu.Unlock()
	})
	row := el("div", "kind-buttons")
	row.Set("style", "margin-top:12px")
	add(row, refs, diag)
	add(panel, row)
}

func (a *app) renderTypeInspectorLocked(panel js.Value, ty Type) {
	head := el("div", "inspect-head")
	title := el("div", "inspect-title")
	setText(title, ty.Name)
	sub := el("div", "inspect-sub")
	setText(sub, fmt.Sprintf("%s · lines %d–%d", ty.Kind, ty.StartLine, ty.EndLine))
	add(head, title, sub)
	add(panel, head)
	if len(ty.Fields) > 0 {
		label := el("div", "section-label")
		setText(label, "Fields")
		add(panel, label)
		list := el("div", "func-list")
		for _, f := range ty.Fields {
			row := el("div", "func-row")
			name := el("span", "fn-name")
			if f.Embedded {
				setText(name, "↳ "+f.Type)
			} else {
				setText(name, f.Name+"  "+f.Type)
			}
			add(list, row)
		}
		add(panel, list)
	}
	if len(ty.Methods) > 0 {
		label := el("div", "section-label")
		setText(label, "Methods")
		add(panel, label)
		list := el("div", "func-list")
		for _, m := range ty.Methods {
			row := el("div", "func-row")
			name := el("span", "fn-name")
			setText(name, m.Name)
			add(list, row)
		}
		add(panel, list)
	}
	if len(ty.Implements) > 0 {
		label := el("div", "section-label")
		setText(label, "Implements")
		add(panel, label)
		note := el("div", "empty-note")
		setText(note, strings.Join(ty.Implements, ", "))
		add(panel, note)
	}
	if len(ty.Bases) > 0 || len(ty.Embedded) > 0 {
		label := el("div", "section-label")
		setText(label, "Bases / embeds")
		add(panel, label)
		note := el("div", "empty-note")
		setText(note, strings.Join(append(append([]string{}, ty.Bases...), ty.Embedded...), ", "))
		add(panel, note)
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
