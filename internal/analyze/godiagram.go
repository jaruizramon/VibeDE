package analyze

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"vibede/internal/diagram"
)

// buildGoFlowGraph builds either a function control-flow graph (function set)
// or a call map for the whole file (function empty).
func (e *Engine) buildGoFlowGraph(a Analysis, src []byte, function string, maxNodes int) diagram.Graph {
	if function == "" {
		return buildGoCallGraph(a, maxNodes)
	}
	fset := token.NewFileSet()
	file, _ := parser.ParseFile(fset, a.Path, src, parser.ParseComments|parser.AllErrors)
	if file == nil {
		return noteGraph("Flowchart", "Could not parse Go source")
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fd.Name.Name
		if fd.Recv != nil {
			name = receiverBase(fd) + "." + fd.Name.Name
		}
		if name == function || (fn == nil && fd.Name.Name == function) {
			fn = fd
		}
	}
	if fn == nil {
		return noteGraph("Flowchart", "Function "+function+" not found")
	}
	fb := &flowBuilder{
		g:        &diagram.Graph{Title: "Control flow · " + function},
		fset:     fset,
		src:      src,
		maxNodes: maxNodes,
	}
	label := function
	if fn.Recv != nil && !strings.Contains(function, ".") {
		label = receiverBase(fn) + "." + function
	}
	start := fb.node(label, diagram.Start)
	var exits []string
	if fn.Body != nil {
		entries, bodyExits := fb.buildSeq(fn.Body.List, []string{start})
		_ = entries
		exits = bodyExits
	} else {
		exits = []string{start}
	}
	if len(exits) > 0 {
		end := fb.node("end", diagram.End)
		for _, x := range exits {
			fb.link(x, end, "")
		}
	}
	fb.addTruncationNote()
	return *fb.g
}

type flowBuilder struct {
	g         *diagram.Graph
	fset      *token.FileSet
	src       []byte
	next      int
	maxNodes  int
	truncated bool
}

func (fb *flowBuilder) node(label string, kind diagram.NodeKind) string {
	if fb.next >= fb.maxNodes {
		fb.truncated = true
		return ""
	}
	id := "n" + strconv.Itoa(fb.next)
	fb.next++
	fb.g.Add(diagram.Node{ID: id, Label: label, Kind: kind})
	return id
}

func (fb *flowBuilder) link(from, to, label string) {
	if from == "" || to == "" || from == to {
		return
	}
	fb.g.Link(diagram.Edge{From: from, To: to, Label: label, Kind: diagram.Arrow})
}

func (fb *flowBuilder) addTruncationNote() {
	if !fb.truncated {
		return
	}
	id := "limit"
	fb.g.Add(diagram.Node{ID: id, Label: fmt.Sprintf("Diagram truncated at %d nodes", fb.maxNodes), Kind: diagram.Note})
}

func (fb *flowBuilder) text(n ast.Node, max int) string {
	if fb.fset == nil || fb.src == nil || n == nil {
		return ""
	}
	start := fb.fset.Position(n.Pos()).Offset
	end := fb.fset.Position(n.End()).Offset
	if start < 0 || end > len(fb.src) || start >= end {
		return ""
	}
	s := strings.Join(strings.Fields(string(fb.src[start:end])), " ")
	if max > 0 && len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

func isSimpleStmt(st ast.Stmt) bool {
	switch st.(type) {
	case *ast.ExprStmt, *ast.AssignStmt, *ast.IncDecStmt, *ast.DeclStmt,
		*ast.SendStmt, *ast.GoStmt, *ast.DeferStmt:
		return true
	}
	return false
}

func (fb *flowBuilder) simpleText(stmts []ast.Stmt) string {
	parts := make([]string, 0, len(stmts))
	total := 0
	for _, st := range stmts {
		s := fb.text(st, 120)
		if s == "" {
			continue
		}
		parts = append(parts, s)
		total += len(s)
		if total > 190 {
			break
		}
	}
	out := strings.Join(parts, " ; ")
	if len(out) > 200 {
		out = out[:200] + "…"
	}
	return out
}

// buildSeq lays out a statement list. It returns entry nodes (first drawn
// nodes) and exit nodes (paths that may continue to the next statement).
func (fb *flowBuilder) buildSeq(stmts []ast.Stmt, incoming []string) (entries, exits []string) {
	entries = append(entries, incoming...)
	current := append([]string(nil), incoming...)
	var simple []ast.Stmt
	flush := func() {
		if len(simple) == 0 {
			return
		}
		label := fb.simpleText(simple)
		if label == "" {
			simple = nil
			return
		}
		node := fb.node(label, diagram.Process)
		for _, in := range current {
			fb.link(in, node, "")
		}
		current = []string{node}
		if len(entries) == 0 {
			entries = []string{node}
		}
		simple = nil
	}
	for _, st := range stmts {
		if isSimpleStmt(st) || st == nil {
			simple = append(simple, st)
			if len(simple) >= 4 {
				flush()
			}
			continue
		}
		flush()
		e, x := fb.buildStmt(st, current)
		if len(entries) == 0 {
			entries = append(entries, e...)
		}
		current = x
		if len(current) == 0 {
			break
		}
	}
	flush()
	if len(entries) == 0 {
		entries = append(entries, incoming...)
	}
	return entries, current
}

func (fb *flowBuilder) buildStmt(st ast.Stmt, incoming []string) (entries, exits []string) {
	switch t := st.(type) {
	case *ast.IfStmt:
		label := "if " + fb.text(t.Cond, 95)
		decision := fb.node(label, diagram.Decision)
		if decision == "" {
			return nil, incoming
		}
		for _, in := range incoming {
			fb.link(in, decision, "")
		}
		thenIn, thenOut := fb.buildSeq(t.Body.List, nil)
		var allExits []string
		if len(thenIn) > 0 {
			fb.g.Link(diagram.Edge{From: decision, To: thenIn[0], Label: "yes", Kind: diagram.Arrow})
			allExits = append(allExits, thenOut...)
		} else {
			pass := fb.node("yes", diagram.Process)
			fb.link(decision, pass, "yes")
			allExits = append(allExits, pass)
		}
		if t.Else != nil {
			elseEntries, elseExits := fb.buildStmt(t.Else, nil)
			if len(elseEntries) > 0 {
				fb.g.Link(diagram.Edge{From: decision, To: elseEntries[0], Label: "no", Kind: diagram.Arrow})
				allExits = append(allExits, elseExits...)
			} else {
				pass := fb.node("no", diagram.Process)
				fb.link(decision, pass, "no")
				allExits = append(allExits, pass)
			}
		} else {
			pass := fb.node("no", diagram.Process)
			fb.link(decision, pass, "no")
			allExits = append(allExits, pass)
		}
		return []string{decision}, allExits
	case *ast.ForStmt:
		h := fb.headerText(t, t.Body, 120)
		if h == "" {
			h = loopHeaderText(fb, t, "for")
		}
		header := fb.node(h, diagram.Process)
		if header == "" {
			return nil, incoming
		}
		for _, in := range incoming {
			fb.link(in, header, "")
		}
		bodyIn, bodyOut := fb.buildSeq(t.Body.List, nil)
		if len(bodyIn) > 0 {
			fb.link(header, bodyIn[0], "body")
			for _, out := range bodyOut {
				if out != "" {
					fb.g.Link(diagram.Edge{From: out, To: header, Label: "loop", Kind: diagram.Arrow, Dashed: true})
				}
			}
		}
		return []string{header}, []string{header}
	case *ast.RangeStmt:
		header := fb.node(fb.headerText(t, t.Body, 110), diagram.Process)
		if header == "" {
			return nil, incoming
		}
		for _, in := range incoming {
			fb.link(in, header, "")
		}
		bodyIn, bodyOut := fb.buildSeq(t.Body.List, nil)
		if len(bodyIn) > 0 {
			fb.link(header, bodyIn[0], "body")
			for _, out := range bodyOut {
				if out != "" {
					fb.g.Link(diagram.Edge{From: out, To: header, Label: "loop", Kind: diagram.Arrow, Dashed: true})
				}
			}
		}
		return []string{header}, []string{header}
	case *ast.SwitchStmt:
		return fb.buildSwitch("switch "+fb.text(t.Tag, 80), t.Body.List, incoming)
	case *ast.TypeSwitchStmt:
		return fb.buildSwitch("type switch", t.Body.List, incoming)
	case *ast.SelectStmt:
		return fb.buildSwitch("select", t.Body.List, incoming)
	case *ast.BlockStmt:
		return fb.buildSeq(t.List, incoming)
	case *ast.LabeledStmt:
		entries, exits = fb.buildStmt(t.Stmt, incoming)
		return entries, exits
	case *ast.ReturnStmt:
		label := "return"
		if len(t.Results) > 0 {
			label = "return " + fb.text(t.Results[0], 70)
		}
		node := fb.node(label, diagram.End)
		for _, in := range incoming {
			fb.link(in, node, "")
		}
		return []string{node}, nil
	case *ast.BranchStmt:
		node := fb.node(t.Tok.String(), diagram.Note)
		for _, in := range incoming {
			fb.link(in, node, "")
		}
		return []string{node}, []string{node}
	default:
		label := fb.text(st, 150)
		if label == "" {
			return nil, incoming
		}
		node := fb.node(label, diagram.Process)
		for _, in := range incoming {
			fb.link(in, node, "")
		}
		return []string{node}, []string{node}
	}
}

func (fb *flowBuilder) buildSwitch(label string, clauses []ast.Stmt, incoming []string) (entries, exits []string) {
	decision := fb.node(label, diagram.Decision)
	if decision == "" {
		return nil, incoming
	}
	for _, in := range incoming {
		fb.link(in, decision, "")
	}
	var allExits []string
	for _, cl := range clauses {
		var bodies []ast.Stmt
		caseLabel := "case"
		switch c := cl.(type) {
		case *ast.CaseClause:
			bodies = c.Body
			var labels []string
			for _, e := range c.List {
				labels = append(labels, fb.text(e, 30))
			}
			if len(labels) == 0 {
				caseLabel = "default"
			} else {
				caseLabel = strings.Join(labels, ", ")
			}
		case *ast.CommClause:
			bodies = c.Body
			if c.Comm != nil {
				caseLabel = fb.text(c.Comm, 40)
			}
		}
		bin, bout := fb.buildSeq(bodies, nil)
		if len(bin) > 0 {
			fb.g.Link(diagram.Edge{From: decision, To: bin[0], Label: caseLabel, Kind: diagram.Arrow})
			allExits = append(allExits, bout...)
		} else {
			pass := fb.node(caseLabel, diagram.Process)
			fb.g.Link(diagram.Edge{From: decision, To: pass, Label: caseLabel, Kind: diagram.Arrow})
			allExits = append(allExits, pass)
		}
	}
	if len(allExits) == 0 {
		allExits = []string{decision}
	}
	return []string{decision}, allExits
}

func (fb *flowBuilder) headerText(n ast.Node, body *ast.BlockStmt, max int) string {
	if fb.fset == nil || n == nil || body == nil {
		return ""
	}
	start := fb.fset.Position(n.Pos()).Offset
	end := fb.fset.Position(body.Lbrace).Offset
	if start < 0 || end > len(fb.src) || start >= end {
		return ""
	}
	s := strings.Join(strings.Fields(string(fb.src[start:end])), " ")
	if max > 0 && len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

func loopHeaderText(fb *flowBuilder, t *ast.ForStmt, prefix string) string {
	var parts []string
	parts = append(parts, prefix)
	if t.Cond != nil {
		parts = append(parts, fb.text(t.Cond, 70))
	}
	if t.Post != nil {
		parts = append(parts, "· "+fb.text(t.Post, 40))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// ---- call graph ----

func buildGoCallGraph(a Analysis, maxNodes int) diagram.Graph {
	g := diagram.Graph{Title: "Call map · " + a.Path}
	if len(a.Functions) == 0 {
		g.Add(diagram.Node{ID: "empty", Label: "No functions detected", Kind: diagram.Note})
		return g
	}
	type nodeRef struct {
		id     string
		fn     Function
		byName string
	}
	refs := make([]nodeRef, 0, len(a.Functions))
	byName := make(map[string]string)
	byKey := make(map[string]string)
	for _, fn := range a.Functions {
		if len(refs) >= maxNodes {
			break
		}
		id := fmt.Sprintf("f%d", len(refs))
		kind := diagram.Process
		if fn.Name == "main" {
			kind = diagram.Terminal
		}
		label := fn.Name
		if fn.Receiver != "" {
			label = fn.Receiver + "." + fn.Name
		}
		if fn.BigO != "" {
			label += "\n" + bigOPlain(fn.BigO)
		}
		g.Add(diagram.Node{ID: id, Label: label, Kind: kind})
		ref := nodeRef{id: id, fn: fn, byName: fn.Name}
		refs = append(refs, ref)
		if _, ok := byName[fn.Name]; !ok {
			byName[fn.Name] = id
		}
		byKey[functionKey(fn)] = id
	}
	for _, ref := range refs {
		for _, call := range ref.fn.Calls {
			target := ""
			if id, ok := byKey[call]; ok {
				target = id
			} else if id, ok := byName[call]; ok {
				target = id
			}
			if target != "" && target != ref.id {
				g.Link(diagram.Edge{From: ref.id, To: target, Kind: diagram.Arrow})
			}
		}
	}
	if len(a.Functions) > len(refs) {
		g.Add(diagram.Node{ID: "more", Label: fmt.Sprintf("+%d more functions", len(a.Functions)-len(refs)), Kind: diagram.Note})
	}
	return g
}

// bigOPlain uses caret notation for diagram text, where HTML superscripts are
// unavailable and Unicode superscript glyphs render too small.
func bigOPlain(s string) string {
	r := strings.NewReplacer("⁰", "^0", "¹", "^1", "²", "^2", "³", "^3", "⁴", "^4",
		"⁵", "^5", "⁶", "^6", "⁷", "^7", "⁸", "^8", "⁹", "^9", "ⁿ", "^n")
	return r.Replace(s)
}

func functionKey(fn Function) string {
	if fn.Receiver != "" {
		return fn.Receiver + "." + fn.Name
	}
	return fn.Name
}

// ---- class diagram ----

// classFile is one analyzed source file participating in a UML diagram.
type classFile struct {
	Path string
	Src  []byte
	A    Analysis
}

var classIdentRe = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*`)

// buildClassGraph merges types from a package/directory and adds structural
// edges plus type-usage dependencies. Structural edges are fields, embedding,
// inheritance and detected interface implementations; usage edges are
// references inside a type's body, which is important for languages such as
// JavaScript where class relationships live in `new Type()` calls.
func buildClassGraph(files []classFile, maxNodes int) diagram.Graph {
	g := diagram.Graph{Title: "UML types"}
	type ref struct {
		id   string
		t    TypeInfo
		file int
	}
	var refs []ref
	byName := make(map[string]string)
	for fi := range files {
		f := &files[fi]
		for _, t := range f.A.Types {
			if len(refs) >= maxNodes {
				break
			}
			id := fmt.Sprintf("t%d", len(refs))
			stereo := t.Kind
			if stereo == "type" {
				stereo = ""
			}
			if len(files) > 1 {
				stereo = strings.TrimSpace(stereo + " · " + path.Base(f.Path))
			}
			node := diagram.Node{ID: id, Label: t.Name, Kind: diagram.Class, Stereotype: stereo}
			var fields []string
			for _, fld := range t.Fields {
				if fld.Embedded {
					continue
				}
				line := fld.Name + ": " + fld.Type
				fields = append(fields, truncateRunes(line, 70))
				if len(fields) >= 16 {
					fields = append(fields, "…")
					break
				}
			}
			var methods []string
			for _, m := range t.Methods {
				methods = append(methods, methodSnippet(m.Name, m.Signature))
				if len(methods) >= 16 {
					methods = append(methods, "…")
					break
				}
			}
			if len(fields) > 0 {
				node.Sections = append(node.Sections, diagram.Section{Title: "fields", Lines: fields})
			}
			if len(methods) > 0 {
				node.Sections = append(node.Sections, diagram.Section{Title: "methods", Lines: methods})
			}
			g.Add(node)
			refs = append(refs, ref{id: id, t: t, file: fi})
			if _, exists := byName[t.Name]; !exists {
				byName[t.Name] = id
			}
		}
	}
	hasEdge := func(from, to string) bool {
		for _, e := range g.Edges {
			if e.From == from && e.To == to {
				return true
			}
		}
		return false
	}
	for _, r := range refs {
		for _, name := range r.t.Embedded {
			if target, ok := byName[name]; ok && target != r.id {
				g.Link(diagram.Edge{From: r.id, To: target, Label: "embeds", Kind: diagram.Inherit})
			}
		}
		for _, name := range r.t.Bases {
			if target, ok := byName[name]; ok && target != r.id {
				g.Link(diagram.Edge{From: r.id, To: target, Label: "extends", Kind: diagram.Inherit})
			}
		}
		for _, name := range r.t.Implements {
			if target, ok := byName[name]; ok && target != r.id {
				g.Link(diagram.Edge{From: r.id, To: target, Label: "implements", Kind: diagram.Inherit, Dashed: true})
			}
		}
		for _, fld := range r.t.Fields {
			if fld.Embedded {
				continue
			}
			base := diagramBaseType(fld.Type)
			if base == "" {
				continue
			}
			if target, ok := byName[base]; ok && target != r.id {
				g.Link(diagram.Edge{From: r.id, To: target, Kind: diagram.Compose})
			}
		}
	}
	// Usage dependencies. Scan only the type's own source range and use a
	// comment/string-masked copy so prose such as "// Laser hit something"
	// does not create a relationship.
	masks := make([][]string, len(files))
	for fi := range files {
		if style, ok := classLexStyle(files[fi].A.Language); ok {
			masks[fi] = strings.Split(string(maskSource(files[fi].Src, style)), "\n")
		}
	}
	for _, r := range refs {
		if r.t.StartLine <= 0 || r.t.EndLine < r.t.StartLine {
			continue
		}
		lines := masks[r.file]
		if len(lines) == 0 {
			continue
		}
		lo := r.t.StartLine - 1
		hi := r.t.EndLine - 1
		if lo < 0 {
			lo = 0
		}
		if hi >= len(lines) {
			hi = len(lines) - 1
		}
		for li := lo; li <= hi && li < len(lines); li++ {
			for _, ident := range classIdentRe.FindAllString(lines[li], -1) {
				target, ok := byName[ident]
				if !ok || target == r.id {
					continue
				}
				if hasEdge(r.id, target) {
					continue
				}
				g.Link(diagram.Edge{From: r.id, To: target, Label: "uses", Kind: diagram.Dependency})
			}
		}
	}
	if len(refs) == 0 {
		g.Add(diagram.Node{ID: "empty", Label: "No types detected", Kind: diagram.Note})
	}
	totalTypes := 0
	for _, f := range files {
		totalTypes += len(f.A.Types)
	}
	if totalTypes > len(refs) {
		g.Add(diagram.Node{ID: "more", Label: fmt.Sprintf("+%d more types", totalTypes-len(refs)), Kind: diagram.Note})
	}
	return g
}

func classLexStyle(lang string) (lexStyle, bool) {
	switch lang {
	case "python":
		return lexPython, true
	case "javascript", "javascriptreact", "typescript", "typescriptreact":
		return lexJS, true
	case "rust":
		return lexRust, true
	case "c", "cpp", "java", "go":
		return lexC, true
	default:
		return 0, false
	}
}

func methodSnippet(name, sig string) string {
	s := strings.TrimSpace(sig)
	s = strings.TrimPrefix(s, "func ")
	if strings.HasPrefix(s, "(") {
		if i := strings.Index(s, ") "); i >= 0 {
			s = s[i+2:]
		}
	}
	if strings.HasPrefix(s, name) {
		rest := strings.TrimPrefix(s, name)
		return truncateRunes(name+rest, 72)
	}
	return truncateRunes(name, 72)
}

func diagramBaseType(t string) string {
	s := strings.TrimSpace(t)
	s = strings.TrimPrefix(s, "*")
	s = strings.TrimPrefix(s, "[]")
	s = strings.TrimPrefix(s, "...")
	if strings.HasPrefix(s, "map[") {
		if i := strings.LastIndex(s, "]"); i >= 0 {
			s = strings.TrimPrefix(strings.TrimSpace(s[i+1:]), "*")
		}
	}
	if strings.HasPrefix(s, "chan ") || strings.HasPrefix(s, "<-chan ") || strings.HasPrefix(s, "chan<- ") {
		parts := strings.Fields(s)
		if len(parts) > 0 {
			s = parts[len(parts)-1]
		}
	}
	if i := strings.IndexByte(s, '['); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSpace(s)
	for _, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return s
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string(r[:n-1]) + "…"
}

// ---- use case ----

type route struct {
	Method string
	Path   string
	Line   int
}

var (
	pyRouteRe   = regexp.MustCompile(`@(?:[A-Za-z_]\w*)\.(get|post|put|delete|patch|route)\(\s*["']([^"']+)["']`)
	jsRouteRe   = regexp.MustCompile(`\b(?:app|router|server|api)\.(get|post|put|delete|patch|all|use)\(\s*["']([^"']+)["']`)
	rustRouteRe = regexp.MustCompile(`\.route\(\s*"([^"]+)"\s*,\s*(?:get|post|put|delete|patch)`)
)

func detectRoutes(a Analysis, src []byte) []route {
	var routes []route
	switch a.Language {
	case "go":
		fset := token.NewFileSet()
		file, _ := parser.ParseFile(fset, a.Path, src, parser.AllErrors)
		if file != nil {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				method := strings.ToUpper(sel.Sel.Name)
				switch strings.ToLower(sel.Sel.Name) {
				case "handlefunc", "handle", "get", "post", "put", "delete", "patch", "route", "any", "all":
				default:
					return true
				}
				if method == "HANDLEFUNC" || method == "HANDLE" || method == "ROUTE" || method == "ANY" || method == "ALL" {
					method = ""
				}
				for _, arg := range call.Args {
					lit, ok := arg.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					p, err := strconv.Unquote(lit.Value)
					if err != nil {
						continue
					}
					m := method
					path := p
					if fields := strings.SplitN(p, " ", 2); len(fields) == 2 && isHTTPMethod(fields[0]) {
						m = strings.ToUpper(fields[0])
						path = fields[1]
					}
					if !strings.HasPrefix(path, "/") {
						continue
					}
					routes = append(routes, route{Method: m, Path: path, Line: fset.Position(call.Pos()).Line})
					break
				}
				return true
			})
		}
	case "python":
		for i, line := range strings.Split(string(src), "\n") {
			for _, m := range pyRouteRe.FindAllStringSubmatch(line, -1) {
				routes = append(routes, route{Method: strings.ToUpper(m[1]), Path: m[2], Line: i + 1})
			}
		}
	case "javascript", "javascriptreact", "typescript", "typescriptreact":
		for i, line := range strings.Split(string(src), "\n") {
			for _, m := range jsRouteRe.FindAllStringSubmatch(line, -1) {
				method := strings.ToUpper(m[1])
				if method == "USE" || method == "ALL" {
					method = ""
				}
				routes = append(routes, route{Method: method, Path: m[2], Line: i + 1})
			}
		}
	case "rust":
		for i, line := range strings.Split(string(src), "\n") {
			for _, m := range rustRouteRe.FindAllStringSubmatch(line, -1) {
				routes = append(routes, route{Path: m[1], Line: i + 1})
			}
		}
	}
	seen := make(map[string]bool)
	out := routes[:0]
	for _, r := range routes {
		key := r.Method + " " + r.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

func isHTTPMethod(s string) bool {
	switch strings.ToUpper(s) {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "TRACE", "CONNECT":
		return true
	}
	return false
}

func buildUseCaseGraph(a Analysis, src []byte, maxNodes int) diagram.Graph {
	g := diagram.Graph{Title: "Use cases · " + a.Path}
	routes := detectRoutes(a, src)
	if len(routes) > maxNodes-4 {
		routes = routes[:maxNodes-4]
	}
	actorID := "actor"
	actorLabel := "API Client"
	if len(routes) == 0 {
		actorLabel = "Operator"
	}
	g.Add(diagram.Node{ID: actorID, Label: actorLabel, Kind: diagram.Actor})
	g.Groups = append(g.Groups, diagram.Group{ID: "system", Label: "Application"})
	if len(routes) > 0 {
		for i, r := range routes {
			id := fmt.Sprintf("uc%d", i)
			label := r.Path
			if r.Method != "" {
				label = r.Method + " " + r.Path
			}
			g.Add(diagram.Node{ID: id, Label: label, Kind: diagram.UseCase, Group: "system"})
			g.Link(diagram.Edge{From: actorID, To: id, Kind: diagram.Association})
		}
		return g
	}
	// No HTTP surface: use entrypoints as operator-facing use cases.
	var entries []Function
	for _, fn := range a.Functions {
		if strings.EqualFold(fn.Name, "main") || strings.HasSuffix(fn.Name, "Handler") ||
			strings.HasSuffix(fn.Name, "handler") || strings.HasSuffix(fn.Name, "Main") {
			entries = append(entries, fn)
		}
	}
	if len(entries) == 0 && len(a.Functions) > 0 {
		entries = append(entries, a.Functions[0])
	}
	if len(entries) == 0 {
		g.Add(diagram.Node{ID: "none", Label: "No routes or entrypoints detected", Kind: diagram.Note, Group: "system"})
		return g
	}
	for i, fn := range entries {
		id := fmt.Sprintf("uc%d", i)
		label := fn.Name
		if fn.Receiver != "" {
			label = fn.Receiver + "." + fn.Name
		}
		g.Add(diagram.Node{ID: id, Label: label, Kind: diagram.UseCase, Group: "system"})
		g.Link(diagram.Edge{From: actorID, To: id, Kind: diagram.Association})
	}
	return g
}

// ---- sequence ----

func (e *Engine) buildSequence(a Analysis, function string, maxMessages int) diagram.Sequence {
	seq := diagram.Sequence{Title: "Call sequence · " + a.Path}
	if len(a.Functions) == 0 {
		return seq
	}
	byKey := make(map[string]Function)
	byName := make(map[string]Function)
	for _, fn := range a.Functions {
		byKey[functionKey(fn)] = fn
		if _, ok := byName[fn.Name]; !ok {
			byName[fn.Name] = fn
		}
	}
	root, ok := byKey[function]
	if !ok {
		root, ok = byName[function]
	}
	if !ok {
		if fn, found := byName["main"]; found && function == "" {
			root = fn
		} else if function == "" {
			root = a.Functions[0]
		} else {
			seq.Title = "Call sequence · " + function + " (not found)"
			return seq
		}
	}
	addPart := func(fn Function) string {
		id := functionKey(fn)
		for _, p := range seq.Participants {
			if p.ID == id {
				return id
			}
		}
		label := fn.Name
		if fn.Receiver != "" {
			label = fn.Receiver + "." + fn.Name
		}
		seq.Participants = append(seq.Participants, diagram.Participant{ID: id, Label: label})
		return id
	}
	addPart(root)
	var visit func(fn Function, depth int, stack map[string]bool)
	visit = func(fn Function, depth int, stack map[string]bool) {
		if depth > 2 || len(seq.Messages) >= maxMessages {
			return
		}
		from := functionKey(fn)
		for _, call := range fn.Calls {
			if len(seq.Messages) >= maxMessages {
				return
			}
			target, ok := byName[call]
			if !ok {
				continue
			}
			to := addPart(target)
			seq.Messages = append(seq.Messages, diagram.Message{From: from, To: to, Label: target.Name, Kind: "sync"})
			if target.Name == fn.Name && target.Receiver == fn.Receiver {
				continue
			}
			if !stack[to] {
				stack[to] = true
				visit(target, depth+1, stack)
				delete(stack, to)
			}
			if len(seq.Messages) < maxMessages {
				seq.Messages = append(seq.Messages, diagram.Message{From: to, To: from, Label: "return", Kind: "return"})
			}
		}
	}
	visit(root, 0, map[string]bool{functionKey(root): true})
	if len(seq.Messages) == 0 {
		seq.Messages = append(seq.Messages, diagram.Message{
			From: functionKey(root), To: functionKey(root), Label: "no internal calls", Kind: "sync",
		})
	}
	return seq
}

// ---- modules ----

func buildModuleGraph(a Analysis, maxNodes int) diagram.Graph {
	g := diagram.Graph{Title: "Imports · " + a.Path}
	self := "self"
	name := a.Path
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	g.Add(diagram.Node{ID: self, Label: name, Kind: diagram.Terminal})
	g.Groups = append(g.Groups, diagram.Group{ID: "imports", Label: "Imports"})
	if len(a.Imports) == 0 {
		g.Add(diagram.Node{ID: "none", Label: "No imports detected", Kind: diagram.Note, Group: "imports"})
		return g
	}
	for i, imp := range a.Imports {
		if i >= maxNodes {
			break
		}
		id := fmt.Sprintf("i%d", i)
		g.Add(diagram.Node{ID: id, Label: imp, Kind: diagram.Process, Group: "imports"})
		g.Link(diagram.Edge{From: self, To: id, Kind: diagram.Dependency})
	}
	if len(a.Imports) > maxNodes {
		g.Add(diagram.Node{ID: "more", Label: fmt.Sprintf("+%d more imports", len(a.Imports)-maxNodes), Kind: diagram.Note, Group: "imports"})
	}
	return g
}

// ---- HTML ----

func buildHTMLGraph(a Analysis, maxNodes int) diagram.Graph {
	g := diagram.Graph{Title: "DOM outline · " + a.Path}
	if len(a.HTML) == 0 {
		g.Add(diagram.Node{ID: "none", Label: "No HTML elements detected", Kind: diagram.Note})
		return g
	}
	type parent struct {
		depth int
		id    string
	}
	var stack []parent
	for i, el := range a.HTML {
		if i >= maxNodes {
			g.Add(diagram.Node{ID: "more", Label: fmt.Sprintf("+%d more elements", len(a.HTML)-maxNodes), Kind: diagram.Note})
			break
		}
		id := fmt.Sprintf("h%d", i)
		label := "<" + el.Tag + ">"
		if el.ID != "" {
			label = "<" + el.Tag + " #" + el.ID + ">"
		} else if len(el.Classes) > 0 {
			label = "<" + el.Tag + " ." + strings.Join(el.Classes, ".") + ">"
		}
		if el.Text != "" {
			label += " " + truncateRunes(el.Text, 28)
		}
		g.Add(diagram.Node{ID: id, Label: label, Kind: diagram.Process})
		for len(stack) > 0 && stack[len(stack)-1].depth >= el.Depth {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			g.Link(diagram.Edge{From: stack[len(stack)-1].id, To: id, Kind: diagram.Association})
		}
		stack = append(stack, parent{depth: el.Depth, id: id})
	}
	return g
}

func noteGraph(title, msg string) diagram.Graph {
	g := diagram.Graph{Title: title}
	g.Add(diagram.Node{ID: "note", Label: msg, Kind: diagram.Note})
	return g
}
