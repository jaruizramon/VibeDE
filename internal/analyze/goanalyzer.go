package analyze

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

func analyzeGo(path string, src []byte) (Analysis, error) {
	a := Analysis{
		Path:      path,
		Language:  "go",
		Bytes:     int64(len(src)),
		Lines:     countLines(src),
		SLOC:      countSLOC(src),
		Functions: []Function{},
		Types:     []TypeInfo{},
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.AllErrors)
	if file == nil {
		return a, err
	}
	if err != nil {
		a.Notes = append(a.Notes, "parse error: "+err.Error())
	}
	imports := make(map[string]bool)
	for _, imp := range file.Imports {
		if p, uerr := strconv.Unquote(imp.Path.Value); uerr == nil {
			imports[p] = true
		}
	}
	for p := range imports {
		a.Imports = append(a.Imports, p)
	}
	sort.Strings(a.Imports)
	if len(a.Imports) == 0 {
		a.Imports = nil
	}

	types := make(map[string]*TypeInfo)
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			ti := TypeInfo{
				Name:      ts.Name.Name,
				StartLine: fset.Position(ts.Pos()).Line,
				EndLine:   fset.Position(ts.End()).Line,
				Exported:  ast.IsExported(ts.Name.Name),
			}
			switch t := ts.Type.(type) {
			case *ast.StructType:
				ti.Kind = "struct"
				for _, f := range t.Fields.List {
					ti.Fields = append(ti.Fields, goField(fset, f))
					if len(f.Names) == 0 {
						if base := baseTypeName(f.Type); base != "" {
							ti.Embedded = append(ti.Embedded, base)
						}
					}
				}
			case *ast.InterfaceType:
				ti.Kind = "interface"
				for _, f := range t.Methods.List {
					if len(f.Names) == 0 {
						if base := baseTypeName(f.Type); base != "" {
							ti.Embedded = append(ti.Embedded, base)
						}
						continue
					}
					for _, n := range f.Names {
						ti.Methods = append(ti.Methods, Method{
							Name:      n.Name,
							Signature: nodeString(fset, f.Type),
						})
					}
				}
			default:
				ti.Kind = "type"
				ti.Fields = append(ti.Fields, Field{Name: "underlying", Type: nodeString(fset, ts.Type)})
			}
			types[ts.Name.Name] = &ti
		}
	}

	// Collect declarations first, so call composition has a complete table.
	freeFuncs := make(map[string]*ast.FuncDecl)
	methodNames := make(map[string][]*ast.FuncDecl)
	decls := make([]*ast.FuncDecl, 0)
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		decls = append(decls, fd)
		if fd.Recv == nil {
			if _, exists := freeFuncs[fd.Name.Name]; !exists {
				freeFuncs[fd.Name.Name] = fd
			}
		} else {
			methodNames[fd.Name.Name] = append(methodNames[fd.Name.Name], fd)
			base := receiverBase(fd)
			if ti := types[base]; ti != nil {
				ti.Methods = append(ti.Methods, Method{
					Name:      fd.Name.Name,
					Signature: signatureText(fset, src, fd),
				})
			}
		}
	}

	ga := &goBigO{
		fset:     fset,
		src:      src,
		free:     freeFuncs,
		methods:  methodNames,
		memo:     make(map[string]BigOResult),
		visiting: make(map[string]bool),
	}
	localNames := make(map[string]bool, len(decls))
	var complexities []Complexity
	for _, fd := range decls {
		localNames[fd.Name.Name] = true
	}

	for _, fd := range decls {
		fn := Function{
			Name:       fd.Name.Name,
			Kind:       "function",
			Signature:  signatureText(fset, src, fd),
			StartLine:  fset.Position(fd.Pos()).Line,
			EndLine:    fset.Position(fd.End()).Line,
			StartChar:  fset.Position(fd.Pos()).Column - 1,
			EndChar:    fset.Position(fd.End()).Column,
			Exported:   ast.IsExported(fd.Name.Name),
			Complexity: 1,
		}
		if fd.Recv != nil {
			fn.Kind = "method"
			fn.Receiver = receiverBase(fd)
		}
		if fd.Body != nil {
			fn.Complexity = cyclomatic(fd.Body)
			res := ga.analyze(fd)
			fn.BigO = res.C.String()
			fn.Evidence = res.Evidence
			fn.Recursive = res.Recursive
			fn.Calls = res.Calls
			complexities = append(complexities, res.C)
		} else {
			fn.BigO = "O(1)"
			complexities = append(complexities, O1())
		}
		fn.Calls = filterCalls(fn.Calls, localNames)
		a.Functions = append(a.Functions, fn)
	}
	sort.SliceStable(a.Functions, func(i, j int) bool {
		if a.Functions[i].StartLine != a.Functions[j].StartLine {
			return a.Functions[i].StartLine < a.Functions[j].StartLine
		}
		return a.Functions[i].Name < a.Functions[j].Name
	})
	for name, ti := range types {
		_ = name
		a.Types = append(a.Types, *ti)
	}
	sort.SliceStable(a.Types, func(i, j int) bool {
		if a.Types[i].StartLine != a.Types[j].StartLine {
			return a.Types[i].StartLine < a.Types[j].StartLine
		}
		return a.Types[i].Name < a.Types[j].Name
	})
	computeImplements(a.Types)
	a.Metrics.Types = len(a.Types)
	computeMetrics(&a, complexities)
	a.Notes = append(a.Notes,
		"Big-O is a static estimate from loop nesting, recursion shapes and known library costs; runtime data can differ.")
	return a, nil
}

func signatureText(fset *token.FileSet, src []byte, fd *ast.FuncDecl) string {
	start := fset.Position(fd.Type.Pos()).Offset
	var end int
	if fd.Body != nil {
		end = fset.Position(fd.Body.Lbrace).Offset
	} else {
		end = fset.Position(fd.Type.End()).Offset
	}
	if start < 0 || end > len(src) || start >= end {
		return "func " + fd.Name.Name
	}
	sig := strings.TrimSpace(string(src[start:end]))
	sig = strings.Join(strings.Fields(sig), " ")
	if len(sig) > 240 {
		sig = sig[:240] + "…"
	}
	return sig
}

func goField(fset *token.FileSet, f *ast.Field) Field {
	out := Field{Type: nodeString(fset, f.Type)}
	if len(f.Names) > 0 {
		names := make([]string, 0, len(f.Names))
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
		out.Name = strings.Join(names, ", ")
	} else {
		out.Embedded = true
		out.Name = baseTypeName(f.Type)
	}
	if f.Tag != nil {
		if tag, err := strconv.Unquote(f.Tag.Value); err == nil {
			out.Tag = tag
		}
	}
	return out
}

func nodeString(fset *token.FileSet, n ast.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	_ = printer.Fprint(&b, fset, n)
	s := strings.Join(strings.Fields(b.String()), " ")
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}

func baseTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return baseTypeName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return baseTypeName(t.X)
	case *ast.IndexListExpr:
		return baseTypeName(t.X)
	case *ast.ParenExpr:
		return baseTypeName(t.X)
	}
	return ""
}

func receiverBase(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	return baseTypeName(fd.Recv.List[0].Type)
}

func computeImplements(types []TypeInfo) {
	interfaces := make(map[string]map[string]bool)
	for _, t := range types {
		if t.Kind != "interface" {
			continue
		}
		set := make(map[string]bool, len(t.Methods))
		for _, m := range t.Methods {
			set[m.Name] = true
		}
		if len(set) > 0 {
			interfaces[t.Name] = set
		}
	}
	if len(interfaces) == 0 {
		return
	}
	names := make([]string, 0, len(interfaces))
	for n := range interfaces {
		names = append(names, n)
	}
	sort.Strings(names)
	for i := range types {
		if types[i].Kind != "struct" {
			continue
		}
		methodSet := make(map[string]bool, len(types[i].Methods))
		for _, m := range types[i].Methods {
			methodSet[m.Name] = true
		}
		for _, iname := range names {
			need := interfaces[iname]
			ok := true
			for m := range need {
				if !methodSet[m] {
					ok = false
					break
				}
			}
			if ok {
				types[i].Implements = append(types[i].Implements, iname)
			}
		}
		sort.Strings(types[i].Implements)
	}
}

func computeMetrics(a *Analysis, complexities []Complexity) {
	a.Metrics.Functions = len(a.Functions)
	total := 0
	max := 0
	var dominant Complexity
	haveDominant := false
	for i, f := range a.Functions {
		total += f.Complexity
		if f.Complexity > max {
			max = f.Complexity
		}
		if i < len(complexities) {
			c := complexities[i]
			if !haveDominant {
				dominant = c
				haveDominant = true
			} else {
				dominant = dominant.Max(c)
			}
		}
	}
	a.Metrics.MaxComplexity = max
	a.Metrics.TotalComplexity = total
	if len(a.Functions) > 0 {
		a.Metrics.AvgComplexity = (total + len(a.Functions)/2) / len(a.Functions)
	}
	if haveDominant {
		a.Metrics.DominantBigO = dominant.String()
	} else {
		a.Metrics.DominantBigO = bigONone
	}
}

func filterCalls(calls []string, local map[string]bool) []string {
	if len(calls) == 0 {
		return nil
	}
	out := make([]string, 0, len(calls))
	seen := make(map[string]bool, len(calls))
	for _, c := range calls {
		if !local[c] || seen[c] || c == "" {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func cyclomatic(n ast.Node) int {
	cc := 1
	ast.Inspect(n, func(node ast.Node) bool {
		switch t := node.(type) {
		case *ast.IfStmt:
			cc++
		case *ast.ForStmt, *ast.RangeStmt:
			cc++
		case *ast.CaseClause:
			if len(t.List) > 0 {
				cc++
			}
		case *ast.CommClause:
			cc++
		case *ast.BinaryExpr:
			if t.Op == token.LAND || t.Op == token.LOR {
				cc++
			}
		}
		return true
	})
	return cc
}

func countLines(src []byte) int {
	if len(src) == 0 {
		return 0
	}
	return bytes.Count(src, []byte("\n")) + 1
}

func countSLOC(src []byte) int {
	lines := strings.Split(string(src), "\n")
	inBlock := false
	n := 0
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		if inBlock {
			if idx := strings.Index(s, "*/"); idx >= 0 {
				inBlock = false
				s = strings.TrimSpace(s[idx+2:])
				if s == "" {
					continue
				}
			} else {
				continue
			}
		}
		if strings.HasPrefix(s, "//") || strings.HasPrefix(s, "#") {
			continue
		}
		if strings.HasPrefix(s, "/*") {
			if idx := strings.Index(s, "*/"); idx >= 0 {
				s = strings.TrimSpace(s[idx+2:])
				if s == "" {
					continue
				}
			} else {
				inBlock = true
				continue
			}
		}
		n++
	}
	return n
}

// ---- Big-O ----

// BigOResult is one function's complexity estimate and its local call graph.
type BigOResult struct {
	C         Complexity
	Evidence  []string
	Recursive bool
	Calls     []string
}

type goBigO struct {
	fset     *token.FileSet
	src      []byte
	free     map[string]*ast.FuncDecl
	methods  map[string][]*ast.FuncDecl
	memo     map[string]BigOResult
	visiting map[string]bool
}

func (g *goBigO) analyze(fd *ast.FuncDecl) BigOResult {
	key := fd.Name.Name
	if fd.Recv != nil {
		key = receiverBase(fd) + "." + fd.Name.Name
	}
	if r, ok := g.memo[key]; ok {
		return r
	}
	if g.visiting[key] {
		return BigOResult{C: O1()}
	}
	g.visiting[key] = true
	defer delete(g.visiting, key)

	c := O1()
	var evidence []string
	if fd.Body != nil {
		c, evidence = g.block(fd.Body.List)
	}
	rec := g.recursion(fd)
	if rec.Recursive {
		c = c.Max(rec.C)
		evidence = append(evidence, rec.Evidence...)
	}
	calls := g.collectCalls(fd)
	if c.IsConstant() && !rec.Recursive && len(evidence) == 0 {
		evidence = append(evidence, "no loops or recursion detected → O(1)")
	}
	if len(evidence) > 30 {
		evidence = evidence[:30]
	}
	r := BigOResult{C: c, Evidence: evidence, Recursive: rec.Recursive, Calls: calls}
	g.memo[key] = r
	return r
}

func (g *goBigO) line(n ast.Node) int { return g.fset.Position(n.Pos()).Line }

func (g *goBigO) block(stmts []ast.Stmt) (Complexity, []string) {
	c := O1()
	var ev []string
	for _, st := range stmts {
		sc, sev := g.stmt(st)
		c = c.Max(sc)
		ev = append(ev, sev...)
	}
	return c, ev
}

func (g *goBigO) stmt(st ast.Stmt) (Complexity, []string) {
	switch t := st.(type) {
	case nil:
		return O1(), nil
	case *ast.BlockStmt:
		return g.block(t.List)
	case *ast.ExprStmt:
		return g.expr(t.X)
	case *ast.AssignStmt:
		c := O1()
		var ev []string
		for _, r := range t.Rhs {
			rc, rev := g.expr(r)
			c = c.Max(rc)
			ev = append(ev, rev...)
		}
		return c, ev
	case *ast.DeclStmt:
		if gd, ok := t.Decl.(*ast.GenDecl); ok {
			c := O1()
			var ev []string
			for _, spec := range gd.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					for _, v := range vs.Values {
						vc, vev := g.expr(v)
						c = c.Max(vc)
						ev = append(ev, vev...)
					}
				}
			}
			return c, ev
		}
		return O1(), nil
	case *ast.ReturnStmt:
		c := O1()
		var ev []string
		for _, r := range t.Results {
			rc, rev := g.expr(r)
			c = c.Max(rc)
			ev = append(ev, rev...)
		}
		return c, ev
	case *ast.IfStmt:
		c := O1()
		var ev []string
		if t.Init != nil {
			ic, iev := g.stmt(t.Init)
			c = c.Max(ic)
			ev = append(ev, iev...)
		}
		if t.Cond != nil {
			cc, cev := g.expr(t.Cond)
			c = c.Max(cc)
			ev = append(ev, cev...)
		}
		bc, bev := g.block(t.Body.List)
		c = c.Max(bc)
		ev = append(ev, bev...)
		if t.Else != nil {
			ec, eev := g.stmt(t.Else)
			c = c.Max(ec)
			ev = append(ev, eev...)
		}
		return c, ev
	case *ast.ForStmt:
		factor, desc := g.forFactor(t)
		bc, bev := g.block(t.Body.List)
		c := bc.Mul(factor)
		ev := append([]string{fmt.Sprintf("%s at line %d", desc, g.line(t))}, bev...)
		if t.Init != nil {
			ic, iev := g.stmt(t.Init)
			c = c.Max(ic)
			ev = append(ev, iev...)
		}
		if t.Cond != nil {
			cc, cev := g.expr(t.Cond)
			c = c.Max(cc)
			ev = append(ev, cev...)
		}
		if t.Post != nil {
			pc, pev := g.stmt(t.Post)
			c = c.Max(pc)
			ev = append(ev, pev...)
		}
		return c, ev
	case *ast.RangeStmt:
		factor := ON()
		desc := "range loop → O(n)"
		if lit, ok := t.X.(*ast.BasicLit); ok && lit.Kind == token.INT {
			if n, err := strconv.Atoi(lit.Value); err == nil && n <= 64 {
				factor = O1()
				desc = "range over a small constant → O(1)"
			}
		}
		bc, bev := g.block(t.Body.List)
		c := bc.Mul(factor)
		ev := []string{fmt.Sprintf("%s at line %d", desc, g.line(t))}
		ev = append(ev, bev...)
		return c, ev
	case *ast.SwitchStmt:
		c := O1()
		var ev []string
		if t.Init != nil {
			ic, iev := g.stmt(t.Init)
			c = c.Max(ic)
			ev = append(ev, iev...)
		}
		if t.Tag != nil {
			tc, tev := g.expr(t.Tag)
			c = c.Max(tc)
			ev = append(ev, tev...)
		}
		for _, cl := range t.Body.List {
			cc, cev := g.stmt(cl)
			c = c.Max(cc)
			ev = append(ev, cev...)
		}
		return c, ev
	case *ast.TypeSwitchStmt:
		c := O1()
		var ev []string
		if t.Init != nil {
			ic, iev := g.stmt(t.Init)
			c = c.Max(ic)
			ev = append(ev, iev...)
		}
		if t.Assign != nil {
			ac, aev := g.stmt(t.Assign)
			c = c.Max(ac)
			ev = append(ev, aev...)
		}
		for _, cl := range t.Body.List {
			cc, cev := g.stmt(cl)
			c = c.Max(cc)
			ev = append(ev, cev...)
		}
		return c, ev
	case *ast.SelectStmt:
		c := O1()
		var ev []string
		for _, cl := range t.Body.List {
			cc, cev := g.stmt(cl)
			c = c.Max(cc)
			ev = append(ev, cev...)
		}
		return c, ev
	case *ast.CaseClause:
		return g.block(t.Body)
	case *ast.CommClause:
		c := O1()
		var ev []string
		if t.Comm != nil {
			cc, cev := g.stmt(t.Comm)
			c = c.Max(cc)
			ev = append(ev, cev...)
		}
		bc, bev := g.block(t.Body)
		c = c.Max(bc)
		ev = append(ev, bev...)
		return c, ev
	case *ast.LabeledStmt:
		return g.stmt(t.Stmt)
	case *ast.DeferStmt:
		return g.expr(t.Call)
	case *ast.GoStmt:
		// The goroutine's work does not delay the caller. Count only the
		// expression evaluation needed to launch it.
		return g.expr(t.Call.Fun)
	case *ast.SendStmt:
		c1, e1 := g.expr(t.Chan)
		c2, e2 := g.expr(t.Value)
		return c1.Max(c2), append(e1, e2...)
	case *ast.IncDecStmt:
		return O1(), nil
	case *ast.BranchStmt, *ast.EmptyStmt:
		return O1(), nil
	}
	return O1(), nil
}

func (g *goBigO) forFactor(f *ast.ForStmt) (Complexity, string) {
	if constantForBound(f) {
		return O1(), "bounded loop over a constant → O(1)"
	}
	if halvingPost(f.Post) {
		return OLogN(), "halving loop → O(log n)"
	}
	if f.Cond != nil && g.binarySearchBody(f) {
		return OLogN(), "binary-search style loop → O(log n)"
	}
	return ON(), "loop → O(n)"
}

func constantForBound(f *ast.ForStmt) bool {
	if f.Cond == nil || f.Init == nil {
		return false
	}
	as, ok := f.Init.(*ast.AssignStmt)
	if !ok || len(as.Rhs) == 0 {
		return false
	}
	// A bounded loop must start at a constant; `for i := n; i > 0; i /= 2`
	// looks like a constant lower bound but is logarithmic.
	for _, rhs := range as.Rhs {
		if !isConstantExpr(rhs) {
			return false
		}
	}
	be, ok := f.Cond.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	switch be.Op {
	case token.LSS, token.LEQ, token.GTR, token.GEQ, token.NEQ:
	default:
		return false
	}
	return isConstantExpr(be.Y) || isConstantExpr(be.X)
}

func isConstantExpr(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.BasicLit:
		return x.Kind == token.INT
	case *ast.UnaryExpr:
		return isConstantExpr(x.X)
	case *ast.ParenExpr:
		return isConstantExpr(x.X)
	}
	return false
}

func halvingPost(post ast.Stmt) bool {
	as, ok := post.(*ast.AssignStmt)
	if !ok {
		return false
	}
	halvingLit := func(e ast.Expr) bool {
		be, ok := e.(*ast.BinaryExpr)
		if !ok {
			return false
		}
		if be.Op == token.SHR {
			return true
		}
		if be.Op != token.QUO {
			return false
		}
		lit, ok := be.Y.(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			return false
		}
		n, err := strconv.Atoi(lit.Value)
		return err == nil && n >= 2
	}
	doublingLit := func(e ast.Expr) bool {
		be, ok := e.(*ast.BinaryExpr)
		if !ok {
			return false
		}
		if be.Op == token.SHL {
			return true
		}
		if be.Op != token.MUL {
			return false
		}
		lit, ok := be.Y.(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			return false
		}
		n, err := strconv.Atoi(lit.Value)
		return err == nil && n >= 2
	}
	switch as.Tok {
	case token.QUO_ASSIGN, token.SHR_ASSIGN:
		return true
	case token.MUL_ASSIGN, token.SHL_ASSIGN:
		return true
	case token.ASSIGN:
		if len(as.Rhs) == 1 {
			return halvingLit(as.Rhs[0]) || doublingLit(as.Rhs[0])
		}
	}
	return false
}

func (g *goBigO) binarySearchBody(f *ast.ForStmt) bool {
	if f.Cond == nil {
		return false
	}
	ids := identSet(f.Cond)
	if len(ids) == 0 {
		return false
	}
	midVars := make(map[string]bool)
	assignsBound := false
	hasMidDiv := false
	ast.Inspect(f.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Rhs) == 1 && len(x.Lhs) == 1 {
				if id, ok := x.Lhs[0].(*ast.Ident); ok {
					midVars[id.Name] = true
					if midDivides(x.Rhs[0]) {
						hasMidDiv = true
					}
				}
			}
			for _, lhs := range x.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && ids[id.Name] {
					assignsBound = true
				}
			}
		}
		return true
	})
	if !hasMidDiv || !assignsBound {
		return false
	}
	_ = midVars
	return true
}

func midDivides(e ast.Expr) bool {
	be, ok := e.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if be.Op != token.QUO {
		return false
	}
	lit, ok := be.Y.(*ast.BasicLit)
	if !ok {
		return false
	}
	n, err := strconv.Atoi(lit.Value)
	return err == nil && n == 2
}

func identSet(e ast.Expr) map[string]bool {
	out := make(map[string]bool)
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name != "" {
			out[id.Name] = true
		}
		return true
	})
	return out
}

func (g *goBigO) expr(e ast.Expr) (Complexity, []string) {
	switch t := e.(type) {
	case nil:
		return O1(), nil
	case *ast.CallExpr:
		c, ev := g.call(t)
		for _, a := range t.Args {
			if _, isFuncLit := a.(*ast.FuncLit); isFuncLit {
				continue
			}
			ac, aev := g.expr(a)
			c = c.Max(ac)
			ev = append(ev, aev...)
		}
		return c, ev
	case *ast.BinaryExpr:
		c1, e1 := g.expr(t.X)
		c2, e2 := g.expr(t.Y)
		c := c1.Max(c2)
		if t.Op == token.ADD {
			// String concatenation copies; without type information this is
			// reported as O(n) only when it appears inside a loop, via the
			// enclosing loop factor. Standalone concatenation is O(1) here.
			_ = c
		}
		return c, append(e1, e2...)
	case *ast.UnaryExpr:
		return g.expr(t.X)
	case *ast.ParenExpr:
		return g.expr(t.X)
	case *ast.StarExpr:
		return g.expr(t.X)
	case *ast.KeyValueExpr:
		c1, e1 := g.expr(t.Key)
		c2, e2 := g.expr(t.Value)
		return c1.Max(c2), append(e1, e2...)
	case *ast.CompositeLit:
		c := O1()
		var ev []string
		for _, el := range t.Elts {
			ec, eev := g.expr(el)
			c = c.Max(ec)
			ev = append(ev, eev...)
		}
		return c, ev
	case *ast.FuncLit:
		// A function literal's execution cost is accounted for by the call
		// that receives it; do not double-count it here.
		return O1(), nil
	case *ast.IndexExpr:
		return O1(), nil
	case *ast.IndexListExpr:
		return O1(), nil
	case *ast.SliceExpr:
		return O1(), nil
	case *ast.TypeAssertExpr:
		return O1(), nil
	case *ast.SelectorExpr:
		return O1(), nil
	case *ast.Ident, *ast.BasicLit:
		return O1(), nil
	}
	return O1(), nil
}

func (g *goBigO) call(call *ast.CallExpr) (Complexity, []string) {
	var c Complexity
	var ev []string
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		name := fun.Name
		switch name {
		case "len", "cap", "new", "delete", "clear", "close", "panic", "recover", "print", "println", "min", "max":
			c = O1()
		case "append":
			c = O1()
		case "copy":
			c = ON()
			ev = append(ev, "copy → O(n)")
		case "make":
			c = ON()
			ev = append(ev, "make → O(n) allocation")
		default:
			if fd := g.free[name]; fd != nil {
				if constantCallArgs(call.Args) {
					c = O1()
				} else {
					r := g.analyze(fd)
					c = r.C
					if !c.IsConstant() {
						ev = append(ev, fmt.Sprintf("calls %s() → %s", name, c))
					}
				}
			} else {
				c = O1()
			}
		}
	case *ast.SelectorExpr:
		pkg := ""
		if id, ok := fun.X.(*ast.Ident); ok {
			pkg = id.Name
		}
		c = externalCost(pkg, fun.Sel.Name)
		if !c.IsConstant() {
			ev = append(ev, fmt.Sprintf("%s.%s → %s at line %d", pkg, fun.Sel.Name, c, g.line(call)))
		}
	default:
		c = O1()
	}
	return c, ev
}

// constantCallArgs reports whether a call receives only literal-sized inputs.
// A helper called as f(6) or f([]int{1,2,3}) does not make the caller's cost
// scale with input size, so its body complexity should not be composed into
// the caller's Big-O.
func constantCallArgs(args []ast.Expr) bool {
	if len(args) == 0 {
		return false
	}
	for _, a := range args {
		if !constantExpr(a) {
			return false
		}
	}
	return true
}

func constantExpr(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.BasicLit:
		return true
	case *ast.CompositeLit:
		return true
	case *ast.FuncLit:
		return true
	case *ast.UnaryExpr:
		return constantExpr(t.X)
	case *ast.BinaryExpr:
		return constantExpr(t.X) && constantExpr(t.Y)
	case *ast.ParenExpr:
		return constantExpr(t.X)
	case *ast.Ident:
		return t.Name == "nil" || t.Name == "true" || t.Name == "false"
	}
	return false
}

func externalCost(pkg, name string) Complexity {
	switch pkg {
	case "sort":
		switch name {
		case "Slice", "SliceStable", "Sort", "Stable", "Strings", "Ints", "Float64s":
			return ONLogN()
		case "Search", "SearchInts", "SearchStrings", "SearchFloat64s":
			return OLogN()
		case "IsSorted", "IsSortedFunc":
			return ON()
		}
	case "slices":
		switch name {
		case "Sort", "SortFunc", "SortStableFunc":
			return ONLogN()
		case "BinarySearch", "BinarySearchFunc":
			return OLogN()
		case "Contains", "ContainsFunc", "Index", "IndexFunc", "Clone", "Compact",
			"CompactFunc", "Delete", "Insert", "Replace", "Equal", "EqualFunc", "Reverse":
			return ON()
		}
	case "maps":
		switch name {
		case "Clone", "Keys", "Values", "Collect":
			return ON()
		}
	case "strings", "bytes":
		switch name {
		case "Contains", "ContainsAny", "ContainsRune", "Index", "IndexAny",
			"IndexByte", "IndexRune", "Split", "SplitN", "SplitAfter", "Join",
			"Replace", "ReplaceAll", "ToLower", "ToUpper", "Trim", "TrimSpace",
			"TrimPrefix", "TrimSuffix", "Repeat", "EqualFold", "Count", "Fields":
			return ON()
		}
	case "regexp":
		switch {
		case strings.HasPrefix(name, "Match"), strings.HasPrefix(name, "Find"),
			strings.HasPrefix(name, "Replace"):
			return ON()
		}
	case "encoding/json":
		switch name {
		case "Marshal", "MarshalIndent", "Unmarshal", "NewEncoder", "NewDecoder", "Valid":
			return ON()
		}
	case "os":
		switch name {
		case "ReadFile", "WriteFile", "ReadDir", "Open", "Create":
			return ON()
		}
	case "io":
		switch name {
		case "ReadAll", "Copy", "CopyN", "ReadFull":
			return ON()
		}
	case "path/filepath":
		switch name {
		case "Walk", "WalkDir", "Glob":
			return ON()
		}
	case "bufio":
		switch name {
		case "NewReader", "NewScanner":
			return O1()
		}
	}
	switch name {
	case "Sort", "SortFunc", "SortStableFunc":
		return ONLogN()
	case "Search", "BinarySearch", "BinarySearchFunc":
		return OLogN()
	case "Contains", "ContainsFunc", "Index", "IndexFunc", "Split", "Join",
		"ReadAll", "ReadString", "ReadLine", "Clone", "Compact", "Reverse",
		"ToLower", "ToUpper", "TrimSpace":
		return ON()
	}
	return O1()
}

func (g *goBigO) collectCalls(fd *ast.FuncDecl) []string {
	if fd.Body == nil {
		return nil
	}
	seen := make(map[string]bool)
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			seen[fun.Name] = true
		case *ast.SelectorExpr:
			seen[fun.Sel.Name] = true
		}
		return true
	})
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ---- recursion ----

type recResult struct {
	Recursive bool
	C         Complexity
	Evidence  []string
}

func (g *goBigO) recursion(fd *ast.FuncDecl) recResult {
	if fd.Body == nil {
		return recResult{}
	}
	receiverName := ""
	if fd.Recv != nil && len(fd.Recv.List) > 0 && len(fd.Recv.List[0].Names) > 0 {
		receiverName = fd.Recv.List[0].Names[0].Name
	}
	halvingVars := g.halvingVars(fd.Body)
	var calls []*ast.CallExpr
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if fd.Recv == nil && fun.Name == fd.Name.Name {
				calls = append(calls, call)
			}
		case *ast.SelectorExpr:
			if fun.Sel.Name != fd.Name.Name {
				return true
			}
			if fd.Recv == nil {
				calls = append(calls, call)
				return true
			}
			if id, ok := fun.X.(*ast.Ident); ok && id.Name == receiverName {
				calls = append(calls, call)
			}
		}
		return true
	})
	if len(calls) == 0 {
		return recResult{}
	}
	halving := false
	decrement := false
	linear := false
	for _, call := range calls {
		for _, arg := range call.Args {
			h, d, l := classifyGoRecursiveArg(arg, halvingVars)
			halving = halving || h
			decrement = decrement || d
			linear = linear || l
		}
	}
	var c Complexity
	var shape string
	switch {
	case len(calls) == 1 && halving:
		c, shape = OLogN(), "single halving recursive call → O(log n)"
	case len(calls) >= 2 && halving && !decrement:
		c, shape = ONLogN(), "two divide-and-conquer recursive calls → O(n log n)"
	case len(calls) >= 2 && decrement:
		c, shape = OExp(), "overlapping recursive calls with decremented input → O(2^n)"
	case len(calls) == 1:
		c, shape = ON(), "single recursive call → O(n)"
	case !halving && !decrement && linear:
		c, shape = ON(), "recursive traversal → O(n)"
	default:
		c, shape = ON(), "multiple recursive calls → O(n)"
	}
	line := 0
	if len(calls) > 0 {
		line = g.line(calls[0])
	}
	return recResult{
		Recursive: true,
		C:         c,
		Evidence:  []string{fmt.Sprintf("recursive call at line %d; %s", line, shape)},
	}
}

func (g *goBigO) halvingVars(body *ast.BlockStmt) map[string]bool {
	out := make(map[string]bool)
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		if midDivides(as.Rhs[0]) {
			out[id.Name] = true
		}
		return true
	})
	return out
}

func classifyGoRecursiveArg(e ast.Expr, halvingVars map[string]bool) (halving, decrement, linear bool) {
	switch t := e.(type) {
	case *ast.Ident:
		return halvingVars[t.Name], false, false
	case *ast.ParenExpr:
		return classifyGoRecursiveArg(t.X, halvingVars)
	case *ast.SelectorExpr, *ast.IndexExpr, *ast.StarExpr:
		return false, false, true
	case *ast.SliceExpr:
		h := containsHalvingVar(t.Low, halvingVars) || containsHalvingVar(t.High, halvingVars) || containsHalvingVar(t.Max, halvingVars)
		if h {
			return true, false, false
		}
		return false, false, true
	case *ast.BinaryExpr:
		switch t.Op {
		case token.QUO:
			if n, ok := intLiteral(t.Y); ok && n >= 2 {
				return true, false, false
			}
		case token.SHR:
			return true, false, false
		case token.SUB:
			if n, ok := intLiteral(t.Y); ok && n >= 1 {
				return false, true, false
			}
		case token.ADD:
			// e.g. f(mid+1): keep the halving property of the non-constant
			// operand instead of treating the expression as a traversal.
		case token.MUL:
			if n, ok := intLiteral(t.Y); ok && n >= 2 {
				return false, false, true
			}
		}
		h, d, l := classifyGoRecursiveArg(t.X, halvingVars)
		h2, d2, l2 := classifyGoRecursiveArg(t.Y, halvingVars)
		return h || h2, d || d2, l || l2
	}
	return false, false, false
}

func containsHalvingVar(e ast.Expr, vars map[string]bool) bool {
	if e == nil {
		return false
	}
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && vars[id.Name] {
			found = true
			return false
		}
		return true
	})
	return found
}

func intLiteral(e ast.Expr) (int, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return 0, false
	}
	n, err := strconv.Atoi(lit.Value)
	return n, err == nil
}
