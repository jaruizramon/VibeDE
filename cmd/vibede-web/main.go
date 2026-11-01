//go:build js && wasm

package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"vibede/internal/highlight"
)

type tab struct {
	Path      string
	File      FileResponse
	Analysis  Analysis
	Diags     FileDiagnostics
	Tokens    []highlight.Token
	Scroll    int
	Line      int
	Char      int
	LinesText []string
	Err       string
	Loading   bool
	Loaded    bool
	Highlight map[int]string // 0-based line -> "add" | "del"
}

// pane is an independent editor group with its own tabs and active file.
type pane struct {
	idx          int
	tabs         map[string]*tab
	order        []string
	active       string
	root         js.Value
	tabsEl       js.Value
	toolbarLeft  js.Value
	toolbarRight js.Value
	scroll       js.Value
	code         js.Value
}

type app struct {
	mu sync.Mutex

	info    Info
	root    string
	version string

	expanded map[string]bool
	tree     map[string][]Entry

	panes        []*pane
	activePane   int
	split        bool
	paneRatio    float64
	rightWidth   int
	diagZoom     float64
	diagNaturalW int
	detached     bool
	detachPath   string
	detachLine   int
	openSeq      map[string]int
	refreshOn    map[string]bool
	refreshBusy  map[string]bool
	warned       map[string]bool

	servers []Server
	diags   map[string]FileDiagnostics
	changes []Change
	seen    map[string]bool

	rightTab        string
	diagKind        string
	diagFn          string
	diagMode        string
	diagram         *DiagramDoc
	diagErr         string
	diagLoad        bool
	diagPath        string
	diagKindApplied string
	diagFnApplied   string

	searchQuery     string
	searchCase      bool
	searchResults   []CodeMatch
	searchFilesN    int
	searchScanned   int
	searchTookMS    int64
	searchTruncated bool
	searchLoading   bool
	searchErr       string
	searchGen       int

	selectedFn     *Function
	workspaceEmpty bool
	selectedType   *Type
	references     []NavLocation
	refsFor        string

	filterGen int
	connected bool
	limit     int
	charW     float64

	explain      explainState
	explainPop   js.Value
	explanations []ExplainRecord
	archive      ArchiveInfo
	notes        map[string][]Annotation

	els elements
	es  js.Value
}

type elements struct {
	app           js.Value
	workspace     js.Value
	pathChip      js.Value
	connChip      js.Value
	connDot       js.Value
	connText      js.Value
	serverChip    js.Value
	changeChip    js.Value
	tree          js.Value
	search        js.Value
	outline       js.Value
	servers       js.Value
	panes         js.Value
	paneSplitter  js.Value
	rightSplitter js.Value
	status        js.Value
	rightTabs     js.Value
	rightBody     js.Value
	toasts        js.Value
	helpModal     js.Value

	codeSearchInput   js.Value
	codeSearchResults js.Value
	codeSearchSummary js.Value
	codeSearchCase    js.Value
}

func main() {
	a := &app{
		expanded:    map[string]bool{"": true},
		tree:        make(map[string][]Entry),
		openSeq:     make(map[string]int),
		refreshOn:   make(map[string]bool),
		refreshBusy: make(map[string]bool),
		warned:      make(map[string]bool),
		servers:     nil,
		diags:       make(map[string]FileDiagnostics),
		seen:        make(map[string]bool),
		notes:       make(map[string][]Annotation),
		rightTab:    "activity",
		diagKind:    "flowchart",
		diagMode:    "svg",
		diagZoom:    1,
		paneRatio:   0.5,
		rightWidth:  396,
		limit:       12000,
		charW:       7.5,
	}
	search := strings.TrimPrefix(js.Global().Get("location").Get("search").String(), "?")
	if q, _ := url.ParseQuery(search); q.Get("detached") != "" {
		a.detached = true
		a.detachPath = q.Get("path")
		if n, err := strconv.Atoi(q.Get("line")); err == nil && n > 0 {
			a.detachLine = n - 1
		}
	}
	a.els.app = byID("app")
	a.buildShell(js.Value{})
	a.measureChar()
	a.bindGlobalKeys()
	a.bindExplain()
	go a.boot()
	select {}
}

func (a *app) buildShell(_ js.Value) {
	root := a.els.app
	clear(root)
	shell := el("div", "shell")

	top := el("header", "topbar")
	brand := el("div", "brand")
	mark := el("div", "brand-mark")
	setText(mark, "V")
	name := el("div", "brand-name")
	setText(name, "VibeDE")
	ro := el("div", "ro-badge")
	setText(ro, "READ-ONLY")
	add(brand, mark, name, ro)

	pathChip := el("div", "path-chip")
	pathSpan := el("span", "")
	setText(pathSpan, "loading workspace…")
	add(pathChip, pathSpan)
	a.els.pathChip = pathChip

	actions := el("div", "top-actions")
	conn := el("div", "chip")
	dot := el("span", "dot warn")
	connText := el("span", "")
	setText(connText, "connecting")
	add(conn, dot, connText)
	a.els.connChip = conn
	a.els.connDot = dot
	a.els.connText = connText
	conn.Set("title", "Live event stream status")

	serverChip := el("div", "chip")
	setText(serverChip, "servers —")
	on(serverChip, "click", func(js.Value) { a.focusServers() })
	a.els.serverChip = serverChip

	changeChip := el("div", "chip")
	setText(changeChip, "0 edits")
	on(changeChip, "click", func(js.Value) { a.setRightTab("activity") })
	a.els.changeChip = changeChip

	find := el("button", "icon-btn")
	setText(find, "⌕")
	find.Set("title", "Search code (Ctrl/⌘+F)")
	on(find, "click", func(js.Value) { a.setRightTab("search") })

	help := el("button", "icon-btn")
	setText(help, "?")
	help.Set("title", "Shortcuts and about")
	on(help, "click", func(js.Value) { a.toggleHelp(true) })

	add(actions, conn, serverChip, changeChip, find, help)
	add(top, brand, pathChip, actions)

	ws := el("div", "workspace")
	a.els.workspace = ws

	// Sidebar.
	side := el("aside", "sidebar")

	files := el("section", "side-section files")
	filesTitle := el("div", "side-title")
	filesLabel := el("span", "")
	setText(filesLabel, "Explorer")
	filesSpacer := el("span", "spacer")
	refresh := el("button", "mini-btn")
	setText(refresh, "refresh")
	on(refresh, "click", func(js.Value) { go a.reloadTree() })
	add(filesTitle, filesLabel, filesSpacer, refresh)
	searchWrap := el("div", "search-wrap")
	search := el("input", "search")
	search.Set("placeholder", "filter files…  (Ctrl+K)")
	search.Set("spellcheck", false)
	a.els.search = search
	on(search, "input", func(js.Value) { a.onFilterChanged() })
	add(searchWrap, search)
	tree := el("div", "side-body tree")
	a.els.tree = tree
	add(files, filesTitle, searchWrap, tree)

	outlineSec := el("section", "side-section outline")
	outTitle := el("div", "side-title")
	setText(outTitle, "Structure")
	a.els.outline = el("div", "side-body outline-list")
	add(outlineSec, outTitle, a.els.outline)

	serversSec := el("section", "side-section servers")
	srvTitle := el("div", "side-title")
	setText(srvTitle, "Language servers")
	sp := el("span", "spacer")
	rescan := el("button", "mini-btn")
	setText(rescan, "rescan")
	on(rescan, "click", func(js.Value) { go a.rescan() })
	add(srvTitle, sp, rescan)
	a.els.servers = el("div", "side-body")
	add(serversSec, srvTitle, a.els.servers)

	add(side, files, outlineSec, serversSec)

	// Center: two independent editor groups plus draggable splitters.
	center := el("main", "center")
	panesEl := el("div", "panes")
	a.els.panes = panesEl
	for i := 0; i < 2; i++ {
		idx := i
		p := &pane{idx: i, tabs: make(map[string]*tab)}
		paneEl := el("div", "editor-pane")
		if i == 0 {
			paneEl.Set("className", "editor-pane focused")
		} else {
			paneEl.Set("style", "display:none")
		}
		tabsEl := el("div", "tabs")
		toolbar := el("div", "editor-toolbar")
		left := el("div", "crumbs")
		right := el("div", "toolbar-right")
		add(toolbar, left, right)
		scroll := el("div", "editor-scroll")
		code := el("div", "code")
		add(scroll, code)
		add(paneEl, tabsEl, toolbar, scroll)
		p.root = paneEl
		p.tabsEl = tabsEl
		p.toolbarLeft = left
		p.toolbarRight = right
		p.scroll = scroll
		p.code = code
		a.panes = append(a.panes, p)
		add(panesEl, paneEl)
		if i == 0 {
			splitter := el("div", "pane-splitter")
			splitter.Set("style", "display:none")
			splitter.Set("title", "Drag to resize editor panes")
			on(splitter, "pointerdown", func(ev js.Value) { a.beginPaneDrag(ev) })
			a.els.paneSplitter = splitter
			add(panesEl, splitter)
		}
		on(paneEl, "pointerdown", func(js.Value) { a.focusPane(idx) })
		on(code, "click", func(ev js.Value) { a.onCodeClick(idx, ev) })
		on(scroll, "scroll", func(js.Value) { a.saveScroll(idx); a.hideExplain() })
	}
	status := el("div", "statusbar")
	a.els.status = status
	add(center, panesEl, status)
	// Right panel with a draggable divider.
	rightSplitter := el("div", "v-splitter")
	rightSplitter.Set("title", "Drag to resize the right panel")
	on(rightSplitter, "pointerdown", func(ev js.Value) { a.beginRightDrag(ev) })
	a.els.rightSplitter = rightSplitter
	rightPanel := el("aside", "right")
	rtabs := el("div", "right-tabs")
	a.els.rightTabs = rtabs
	rbody := el("div", "right-body")
	a.els.rightBody = rbody
	add(rightPanel, rtabs, rbody)

	add(ws, side, center, rightSplitter, rightPanel)
	add(shell, top, ws)

	toasts := el("div", "toast-wrap")
	a.els.toasts = toasts
	add(root, shell, toasts)

	a.applyWorkspaceClassLocked()
	a.applyRightWidthLocked()
	a.applySplitLayoutLocked()
	a.renderToolbarLocked(0)
	a.renderToolbarLocked(1)
	a.renderRightTabs()
	a.renderRight()
	a.renderStatus()
	a.renderWelcome()
}

func (a *app) measureChar() {
	canvas := doc().Call("createElement", "canvas")
	ctx := canvas.Call("getContext", "2d")
	ctx.Set("font", `12.5px ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, monospace`)
	w := ctx.Call("measureText", strings.Repeat("M", 20)).Get("width").Float()
	if w > 0 {
		a.charW = w / 20
	}
	if a.charW < 5 || a.charW > 20 {
		a.charW = 7.5
	}
}

func (a *app) boot() {
	info, err := fetchInfo()
	if err != nil {
		a.toast("Cannot reach VibeDE API: "+err.Error(), "err")
		return
	}
	entries, err := fetchTree("")
	if err != nil {
		a.toast("Cannot read workspace tree: "+err.Error(), "err")
	}
	arch, archErr := fetchArchive()
	servers := info.Servers
	a.mu.Lock()
	a.info = info
	a.root = info.Root
	a.version = info.Version
	a.servers = servers
	a.limit = info.Limits.MaxRenderedLines
	if a.limit <= 0 {
		a.limit = 12000
	}
	a.archive = info.Archive
	if archErr == nil {
		a.archive = ArchiveInfo{
			Path:         arch.Path,
			Bytes:        arch.Bytes,
			Changes:      len(arch.Changes),
			Explanations: len(arch.Explanations),
		}
		changes := arch.Changes
		if len(changes) > 200 {
			changes = changes[:200]
		}
		a.changes = changes
		a.explanations = arch.Explanations
	}
	a.tree[""] = entries
	a.mu.Unlock()
	doc().Set("title", "VibeDE · "+shortRoot(info.Root))

	a.mu.Lock()
	a.els.pathChip.Get("firstChild").Set("textContent", info.Root)
	a.renderTreeLocked("")
	a.renderServersLocked()
	a.renderStatus()
	a.renderRightTabs()
	empty := len(entries) == 0
	if empty {
		a.workspaceEmpty = true
	}
	a.mu.Unlock()
	if empty {
		a.renderEmptyWorkspace(info.Root)
	}

	connectEvents(a)

	if a.detached {
		a.mu.Lock()
		a.applyWorkspaceClassLocked()
		a.mu.Unlock()
		if a.detachPath != "" {
			go a.openFile(a.detachPath, a.detachLine, 0, false)
		}
		return
	}

	// Open a README when present, otherwise show the welcome screen.
	var readme string
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		lower := strings.ToLower(e.Name)
		if lower == "readme.md" || lower == "readme" || lower == "readme.txt" || lower == "readme.rst" {
			readme = e.Path
			break
		}
	}
	if readme != "" {
		go a.openFile(readme, 0, 0, false)
	}
}

func shortRoot(root string) string {
	parts := strings.Split(strings.TrimRight(root, "/"), "/")
	if len(parts) <= 2 {
		return root
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

func (a *app) setConnected(ok bool) {
	a.mu.Lock()
	changed := a.connected != ok
	a.connected = ok
	if changed || ok {
		a.els.connDot.Set("className", ternary(ok, "dot ok", "dot err"))
		a.els.connText.Set("textContent", ternary(ok, "live", "reconnecting"))
		a.renderStatus()
	}
	a.mu.Unlock()
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func (a *app) toast(msg, kind string) {
	if msg == "" {
		return
	}
	cls := "toast"
	if kind != "" {
		cls += " " + kind
	}
	node := el("div", cls)
	setText(node, msg)
	add(a.els.toasts, node)
	fn := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		if node.Get("parentNode").Truthy() {
			node.Call("remove")
		}
		return nil
	})
	retained = append(retained, fn)
	js.Global().Call("setTimeout", fn, 5200)
}

func (a *app) bindGlobalKeys() {
	on(doc(), "keydown", func(ev js.Value) {
		key := ev.Get("key").String()
		code := ev.Get("code").String()
		ctrl := ev.Get("ctrlKey").Bool() || ev.Get("metaKey").Bool()
		alt := ev.Get("altKey").Bool()
		shift := ev.Get("shiftKey").Bool()
		stop := func() {
			ev.Call("preventDefault")
			ev.Call("stopPropagation")
		}

		if a.els.helpModal.Truthy() {
			if key == "Escape" || key == "F1" {
				stop()
				a.toggleHelp(false)
			}
			return
		}
		if key == "F1" {
			stop()
			a.toggleHelp(true)
			return
		}
		if key == "Escape" && a.explainPop.Truthy() {
			stop()
			a.hideExplain()
			return
		}
		if ctrl && strings.EqualFold(key, "f") {
			stop()
			if !a.detached {
				a.setRightTab("search")
			}
			return
		}
		if ctrl && strings.EqualFold(key, "e") {
			stop()
			a.ctrlExplain()
			return
		}
		if ctrl && (strings.EqualFold(key, "k") || strings.EqualFold(key, "p")) {
			stop()
			if !a.detached {
				a.els.search.Call("focus")
				a.els.search.Call("select")
			}
			return
		}
		if ctrl && strings.EqualFold(key, "w") {
			stop()
			a.closeActiveTab()
			return
		}
		if ctrl && (code == "Backslash" || key == "\\") {
			stop()
			a.toggleSplit()
			return
		}
		if ctrl && key == "PageDown" {
			stop()
			a.cycleTab(1)
			return
		}
		if ctrl && key == "PageUp" {
			stop()
			a.cycleTab(-1)
			return
		}
		if alt && !ctrl && !shift && !a.detached {
			if tab, ok := rightTabForKey(code, key); ok {
				stop()
				a.setRightTab(tab)
			}
		}
	})
}

// rightTabForKey maps Alt+1…5 to the right-hand panels in display order.
func rightTabForKey(code, key string) (string, bool) {
	tabs := []string{"activity", "problems", "search", "diagrams", "inspector"}
	n := 0
	switch {
	case strings.HasPrefix(code, "Digit"):
		n, _ = strconv.Atoi(strings.TrimPrefix(code, "Digit"))
	case strings.HasPrefix(code, "Numpad"):
		n, _ = strconv.Atoi(strings.TrimPrefix(code, "Numpad"))
	case len(key) == 1 && key[0] >= '1' && key[0] <= '5':
		n = int(key[0] - '0')
	}
	if n < 1 || n > len(tabs) {
		return "", false
	}
	return tabs[n-1], true
}

func (a *app) toggleHelp(show bool) {
	if show {
		if a.els.helpModal.Truthy() {
			a.els.helpModal.Set("style", "display:flex")
			return
		}
		back := el("div", "modal-back")
		modal := el("div", "modal")
		modal.Set("style", "height:auto;max-height:80vh")
		head := el("div", "modal-head")
		title := el("div", "modal-title")
		setText(title, "VibeDE — read-only code intelligence")
		closeBtn := el("button", "close-x")
		setText(closeBtn, "×")
		on(closeBtn, "click", func(js.Value) { a.toggleHelp(false) })
		add(head, title, closeBtn)
		body := el("div", "modal-body")
		body.Set("style", "padding:18px 22px;line-height:1.7;color:var(--muted)")
		intro := el("p", "")
		intro.Set("innerHTML", "VibeDE never edits your workspace. It watches agent writes, asks language servers for diagnostics and navigation, and computes structure, cyclomatic complexity and Big-O statically.")
		body.Call("appendChild", intro)
		list := el("div", "welcome-grid")
		cards := [][2]string{
			{"Ctrl / ⌘ + click", "Go to definition (language server)."},
			{"Alt + click", "Find references; results open in Inspector."},
			{"Ctrl / ⌘ + F", "Find across the workspace (also Ctrl/⌘ + Shift + F)."},
			{"Ctrl / ⌘ + E", "Ask the small model about the selected code (opens a selection popover)."},
			{"Select code", "A popover appears; ask what it does. The model sees this file and its imports."},
			{"Ctrl / ⌘ + K or P", "Focus the file filter."},
			{"Ctrl / ⌘ + W", "Close the active tab."},
			{"Ctrl / ⌘ + \\", "Toggle the split editor."},
			{"Ctrl / ⌘ + PgDn / PgUp", "Next / previous tab."},
			{"Alt + 1…5", "Activity, Problems, Search, Diagrams, Inspector."},
			{"F1", "Open this help; Esc closes it."},
			{"Big-O badges", "Click a badge or an outline row for evidence."},
			{"Diagrams", "Deterministic flowchart, UML, use-case, sequence and import views."},
			{"Live edits", "Agent writes stream into Activity with a line diff."},
			{"History", "Edits and explanations persist in .vibede.rab, gzip-compressed in the workspace root."},
		}
		for _, c := range cards {
			card := el("div", "welcome-card")
			b := el("b", "")
			setText(b, c[0])
			s := el("span", "")
			setText(s, c[1])
			add(card, b, s)
			add(list, card)
		}
		add(body, list)
		add(modal, head, body)
		add(back, modal)
		on(back, "click", func(ev js.Value) {
			if ev.Get("target").Equal(back) {
				a.toggleHelp(false)
			}
		})
		add(doc().Get("body"), back)
		a.els.helpModal = back
		return
	}
	if a.els.helpModal.Truthy() {
		a.els.helpModal.Call("remove")
		a.els.helpModal = js.Value{}
	}
}

func (a *app) focusServers() {
	a.mu.Lock()
	a.renderServersLocked()
	a.mu.Unlock()
	a.els.servers.Call("scrollIntoView", map[string]any{"block": "nearest", "behavior": "smooth"})
}

func (a *app) rescan() {
	servers, err := rescanServers()
	if err != nil {
		a.toast("Server rescan failed: "+apiErrorHint(err), "err")
		return
	}
	a.mu.Lock()
	a.servers = servers
	a.renderServersLocked()
	a.renderStatus()
	a.mu.Unlock()
	a.toast("Language servers rescanned", "")
}

func (a *app) renderStatus() {
	if !a.els.status.Truthy() {
		return
	}
	clear(a.els.status)
	left := el("div", "status-left")
	right := el("div", "status-right")
	addItem := func(parent js.Value, label, value string) {
		item := el("div", "status-item")
		if label != "" {
			setText(item, label+" ")
		}
		b := el("b", "")
		setText(b, value)
		add(item, b)
		add(parent, item)
	}
	addItem(left, "", "READ-ONLY")
	t := a.activeTab()
	if t != nil {
		addItem(left, "lang", langName(t.File.Language))
		line := t.Line + 1
		if line < 1 {
			line = 1
		}
		addItem(left, "ln", fmt.Sprintf("%d", line))
		addItem(left, "lines", fmt.Sprintf("%d", t.File.Lines))
		addItem(left, "functions", fmt.Sprintf("%d", len(t.Analysis.Functions)))
	}
	errs, warns := 0, 0
	for _, fd := range a.diags {
		errs += fd.Counts.Errors
		warns += fd.Counts.Warnings
	}
	if errs > 0 {
		addItem(right, "", fmt.Sprintf("%d errors", errs))
	}
	if warns > 0 {
		addItem(right, "", fmt.Sprintf("%d warnings", warns))
	}
	running := 0
	for _, s := range a.servers {
		if s.Status == "running" {
			running++
		}
	}
	addItem(right, "servers", fmt.Sprintf("%d live", running))
	addItem(right, "", ternary(a.connected, "stream live", "stream down"))
	add(a.els.status, left, right)
}

func (a *app) serverChipText() string {
	running, available, missing := 0, 0, 0
	for _, s := range a.servers {
		switch s.Status {
		case "running", "starting":
			running++
		case "available":
			available++
		case "missing":
			missing++
		}
	}
	parts := []string{fmt.Sprintf("%d live", running)}
	if available > 0 {
		parts = append(parts, fmt.Sprintf("%d ready", available))
	}
	if missing > 0 {
		parts = append(parts, fmt.Sprintf("%d missing", missing))
	}
	return "servers " + strings.Join(parts, " · ")
}

func (a *app) refreshChipsLocked() {
	if a.els.serverChip.Truthy() {
		a.els.serverChip.Set("textContent", a.serverChipText())
	}
	if a.els.changeChip.Truthy() {
		a.els.changeChip.Set("textContent", plural(len(a.changes), "edit", "edits"))
	}
}

func nowLabel(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("15:04:05")
}
