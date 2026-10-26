package analyze

import (
	"crypto/sha256"
	"fmt"
	"path"
	"sort"
	"sync"

	"vibede/internal/config"
	"vibede/internal/diagram"
	"vibede/internal/workspace"
)

type cacheEntry struct {
	hash [sha256.Size]byte
	a    Analysis
}

// Engine caches static analysis per file and builds diagrams.
type Engine struct {
	ws     *workspace.Workspace
	limits config.Limits
	mu     sync.Mutex
	cache  map[string]cacheEntry
}

// NewEngine creates an analysis engine.
func NewEngine(ws *workspace.Workspace, cfg config.Config) *Engine {
	return &Engine{
		ws:     ws,
		limits: cfg.Limits,
		cache:  make(map[string]cacheEntry),
	}
}

// Invalidate forgets a file's cached analysis.
func (e *Engine) Invalidate(rel string) {
	e.mu.Lock()
	delete(e.cache, rel)
	e.mu.Unlock()
}

// Analyze returns structure, complexity and Big-O for one file.
func (e *Engine) Analyze(rel string) (Analysis, error) {
	if _, err := e.ws.Resolve(rel); err != nil {
		return Analysis{}, err
	}
	src, err := e.ws.Read(rel)
	if err != nil {
		return Analysis{}, err
	}
	sum := sha256.Sum256(src)
	e.mu.Lock()
	if c, ok := e.cache[rel]; ok && c.hash == sum {
		e.mu.Unlock()
		return c.a, nil
	}
	e.mu.Unlock()
	lang := workspace.Language(rel)
	a, err := e.analyzeSource(rel, lang, src)
	if err != nil {
		return a, err
	}
	e.mu.Lock()
	e.cache[rel] = cacheEntry{hash: sum, a: a}
	e.mu.Unlock()
	return a, nil
}

func (e *Engine) analyzeSource(rel, lang string, src []byte) (Analysis, error) {
	switch lang {
	case "go":
		return analyzeGo(rel, src)
	case "python", "javascript", "javascriptreact", "typescript", "typescriptreact",
		"rust", "c", "cpp", "java":
		return analyzeGeneric(rel, lang, src)
	case "html":
		return analyzeHTML(rel, src)
	case "css", "scss", "less":
		return analyzeCSS(rel, src)
	default:
		a := Analysis{
			Path:      rel,
			Language:  lang,
			Bytes:     int64(len(src)),
			Lines:     countLines(src),
			SLOC:      countSLOC(src),
			Functions: []Function{},
			Types:     []TypeInfo{},
		}
		a.Metrics.DominantBigO = bigONone
		if lang == "" {
			a.Notes = append(a.Notes, "Unknown language; showing file metadata only.")
		} else {
			a.Notes = append(a.Notes, "No structural analyzer for "+lang+"; use the language server for diagnostics and navigation.")
		}
		return a, nil
	}
}

// Diagram generates a deterministic diagram for a file.
//
// kind is one of: flowchart, class, usecase, sequence, modules, html.
// function selects a function for flowcharts and sequence roots.
func (e *Engine) Diagram(rel, kind, function string) (diagram.Doc, error) {
	if _, err := e.ws.Resolve(rel); err != nil {
		return diagram.Doc{}, err
	}
	src, err := e.ws.Read(rel)
	if err != nil {
		return diagram.Doc{}, err
	}
	a, err := e.Analyze(rel)
	if err != nil {
		return diagram.Doc{}, err
	}
	maxNodes := e.limits.MaxDiagramNodes
	if maxNodes <= 0 {
		maxNodes = 400
	}
	switch kind {
	case "", "flowchart", "callgraph":
		var g diagram.Graph
		if a.Language == "go" && function != "" {
			g = e.buildGoFlowGraph(a, src, function, maxNodes)
		} else if function != "" {
			g = buildFocusCallGraph(a, function, maxNodes)
		} else {
			g = buildCallMap(a, maxNodes)
		}
		doc := diagram.Render(&g, "flowchart")
		doc.Title = g.Title
		return doc, nil
	case "class", "uml":
		files, title := e.classFiles(rel)
		g := buildClassGraph(files, maxNodes)
		g.Title = title
		doc := diagram.Render(&g, "class")
		doc.Title = g.Title
		return doc, nil
	case "usecase":
		g := buildUseCaseGraph(a, src, maxNodes)
		doc := diagram.Render(&g, "usecase")
		doc.Title = g.Title
		return doc, nil
	case "sequence":
		seq := e.buildSequence(a, function, 60)
		return diagram.RenderSequence(seq), nil
	case "modules", "imports":
		g := buildModuleGraph(a, maxNodes)
		doc := diagram.Render(&g, "modules")
		doc.Title = g.Title
		return doc, nil
	case "html", "dom":
		if a.Language != "html" {
			g := noteGraph("DOM outline", "This file is not HTML")
			doc := diagram.Render(&g, "html")
			doc.Title = g.Title
			return doc, nil
		}
		g := buildHTMLGraph(a, maxNodes)
		doc := diagram.Render(&g, "html")
		doc.Title = g.Title
		return doc, nil
	default:
		return diagram.Doc{}, fmt.Errorf("unknown diagram kind %q", kind)
	}
}

// classFiles gathers source files that belong to the same class-diagram scope:
// a Go package, or the current directory for the generic analyzers. Sibling
// files are required for cross-file relationships such as JS `new Player()`
// and Go types split across package files.
func (e *Engine) classFiles(rel string) ([]classFile, string) {
	lang := workspace.Language(rel)
	family := classFamily(lang)
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	paths := []string{rel}
	seen := map[string]bool{rel: true}
	if entries, err := e.ws.List(dir); err == nil {
		for _, en := range entries {
			if en.IsDir || en.Language == "" || seen[en.Path] {
				continue
			}
			if classFamily(en.Language) != family {
				continue
			}
			seen[en.Path] = true
			paths = append(paths, en.Path)
		}
	}
	sort.Strings(paths)
	const maxFiles = 80
	truncated := false
	if len(paths) > maxFiles {
		paths = paths[:maxFiles]
		truncated = true
	}
	files := make([]classFile, 0, len(paths))
	for _, p := range paths {
		a, err := e.Analyze(p)
		if err != nil || len(a.Types) == 0 {
			continue
		}
		src, err := e.ws.Read(p)
		if err != nil {
			continue
		}
		files = append(files, classFile{Path: p, Src: src, A: a})
	}
	title := "UML types · " + rel
	if len(files) > 1 {
		scope := dir
		if scope == "" {
			scope = "workspace root"
		}
		title = fmt.Sprintf("UML types · %s (%d files)", scope, len(files))
		if truncated {
			title += " · truncated"
		}
	}
	return files, title
}

func classFamily(lang string) string {
	switch lang {
	case "javascript", "javascriptreact", "typescript", "typescriptreact":
		return "typescript"
	case "c", "cpp":
		return "c"
	default:
		return lang
	}
}

func buildCallMap(a Analysis, maxNodes int) diagram.Graph {
	g := buildGoCallGraph(a, maxNodes)
	g.Title = "Call map · " + a.Path
	return g
}

func buildFocusCallGraph(a Analysis, focus string, maxNodes int) diagram.Graph {
	g := diagram.Graph{Title: "Call map · " + focus}
	var root *Function
	byKey := make(map[string]*Function)
	byName := make(map[string]*Function)
	for i := range a.Functions {
		fn := &a.Functions[i]
		byKey[functionKey(*fn)] = fn
		if _, ok := byName[fn.Name]; !ok {
			byName[fn.Name] = fn
		}
	}
	if fn, ok := byKey[focus]; ok {
		root = fn
	} else if fn, ok := byName[focus]; ok {
		root = fn
	}
	if root == nil {
		g.Add(diagram.Node{ID: "note", Label: "Function " + focus + " not found", Kind: diagram.Note})
		return g
	}
	add := func(fn *Function) string {
		id := "f:" + functionKey(*fn)
		if g.Node(id) != nil {
			return id
		}
		label := fn.Name
		if fn.Receiver != "" {
			label = fn.Receiver + "." + fn.Name
		}
		if fn.BigO != "" {
			label += "\n" + fn.BigO
		}
		g.Add(diagram.Node{ID: id, Label: label, Kind: diagram.Process})
		return id
	}
	add(root)
	type item struct {
		fn    *Function
		depth int
	}
	queue := []item{{root, 0}}
	seen := map[string]bool{functionKey(*root): true}
	for len(queue) > 0 && len(g.Nodes) < maxNodes {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= 2 {
			continue
		}
		for _, call := range cur.fn.Calls {
			target := byKey[call]
			if target == nil {
				target = byName[call]
			}
			if target == nil {
				continue
			}
			fromID := add(cur.fn)
			toID := add(target)
			if fromID != toID {
				g.Link(diagram.Edge{From: fromID, To: toID, Kind: diagram.Arrow})
			}
			key := functionKey(*target)
			if !seen[key] {
				seen[key] = true
				queue = append(queue, item{target, cur.depth + 1})
			}
		}
	}
	if a.Language != "go" {
		g.Add(diagram.Node{ID: "note", Label: "Control-flow extraction is available for Go; showing the call map", Kind: diagram.Note})
	}
	return g
}
