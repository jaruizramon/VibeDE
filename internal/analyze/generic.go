package analyze

import (
	"sort"
	"strings"
)

// genFamily identifies the syntax family of a generic language.
type genFamily int

const (
	famPython genFamily = iota
	famJS
	famRust
	famC
	famJava
)

func newGenericAnalysis(path, lang string, src []byte) Analysis {
	a := Analysis{
		Path:      path,
		Language:  lang,
		Bytes:     int64(len(src)),
		Lines:     countLines(src),
		SLOC:      countSLOC(src),
		Functions: []Function{},
		Types:     []TypeInfo{},
	}
	return a
}

// analyzeGeneric is the entry point for every non-Go language handled by the
// token/indentation analyzer.
func analyzeGeneric(path, lang string, src []byte) (Analysis, error) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	a := newGenericAnalysis(path, lang, src)
	switch lang {
	case "python":
		parsePython(&a, src)
	case "javascript", "javascriptreact", "typescript", "typescriptreact":
		fam := famJS
		parseBraceLanguage(&a, src, fam, "JavaScript/TypeScript analysis is token-based; JSX, template interpolation and regex literals are handled best-effort.")
	case "rust":
		parseBraceLanguage(&a, src, famRust, "Rust analysis is token-based; macro bodies, generics and lifetimes are handled best-effort.")
	case "c", "cpp":
		parseBraceLanguage(&a, src, famC, "C/C++ analysis is token-based; preprocessor conditionals and macro expansion are not modeled.")
	case "java":
		parseBraceLanguage(&a, src, famJava, "Java analysis is token-based; annotations and generic type parameters are handled best-effort.")
	default:
		a.Notes = append(a.Notes, "unsupported language: "+lang)
		computeMetrics(&a, nil)
		return a, nil
	}
	return a, nil
}

// genericFunc stores parser state for one extracted function.
type genericFunc struct {
	fn        Function
	name      string
	recv      string
	key       string
	bodyLo    int // token indices, inclusive; -1 when unavailable
	bodyHi    int
	bodyStart int // byte offsets of the body
	bodyEnd   int
	lineLo    int
	lineHi    int
	python    bool
	calls     []string
}

// ---------------- Python ----------------

type pyClass struct {
	name    string
	indent  int
	line    int
	endLine int
	bases   []string
	ti      *TypeInfo
}

func parsePython(a *Analysis, src []byte) {
	s := newGenSource(a.Path, a.Language, src, lexPython)
	classes := collectPyClasses(s)
	funcs := collectPyFunctions(s, classes)
	types := make([]*TypeInfo, 0, len(classes))
	for _, c := range classes {
		extractPyMembers(s, c)
		if c.ti.Name != "" {
			types = append(types, c.ti)
		}
	}
	imports := pyImports(s)
	engine := newGenericBigO(s, famPython, funcs)
	finishGeneric(a, s, funcs, types, imports, engine)
	a.Notes = append(a.Notes,
		"Python analysis is indentation-based; decorators are excluded from signatures.",
		"Big-O is a static estimate from loop nesting, comprehension nesting, recursion shapes and known library costs; runtime data can differ.")
	if warn := structuralWarning(s); warn != "" {
		a.Notes = append(a.Notes, warn)
	}
}

func collectPyClasses(s *genSource) []*pyClass {
	var out []*pyClass
	n := s.lineCount()
	for ln := 1; ln <= n; ln++ {
		mt := strings.TrimSpace(s.maskLineString(ln))
		if !strings.HasPrefix(mt, "class ") {
			continue
		}
		rest := mt[len("class "):]
		name := rest
		if idx := strings.IndexAny(name, "(:["); idx >= 0 {
			name = name[:idx]
		}
		name = strings.TrimSpace(name)
		if !isIdentText(name) {
			continue
		}
		indent := s.lineIndent(ln)
		end := pyBlockEnd(s, ln, indent, n)
		ti := &TypeInfo{
			Name:      name,
			Kind:      "class",
			StartLine: ln,
			EndLine:   end,
			Exported:  !strings.HasPrefix(name, "_"),
		}
		if idx := strings.Index(rest, "("); idx >= 0 {
			if close := strings.Index(rest[idx:], ")"); close >= 0 {
				inner := rest[idx+1 : idx+close]
				for _, base := range strings.Split(inner, ",") {
					base = strings.TrimSpace(base)
					if strings.Contains(base, "=") {
						continue // keyword argument such as metaclass=...
					}
					if br := strings.IndexAny(base, "[("); br >= 0 {
						base = strings.TrimSpace(base[:br])
					}
					if base != "" && base != "object" && isIdentPath(base) {
						ti.Bases = append(ti.Bases, base)
					}
				}
			}
		}
		out = append(out, &pyClass{name: name, indent: indent, line: ln, endLine: end, bases: ti.Bases, ti: ti})
	}
	return out
}

func isIdentPath(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if !isIdentText(part) {
			return false
		}
	}
	return true
}

func pyBlockEnd(s *genSource, headerLine, indent, lastLine int) int {
	end := headerLine
	for ln := headerLine + 1; ln <= lastLine; ln++ {
		if strings.TrimSpace(s.lineString(ln)) == "" {
			continue
		}
		if s.lineIndent(ln) <= indent {
			break
		}
		end = ln
	}
	return end
}

func collectPyFunctions(s *genSource, classes []*pyClass) []*genericFunc {
	var out []*genericFunc
	n := s.lineCount()
	seenLine := make(map[int]bool)
	for i := 0; i < len(s.toks); i++ {
		t := s.toks[i]
		if t.text != "def" {
			continue
		}
		if i+1 >= len(s.toks) || !isIdentText(s.toks[i+1].text) {
			continue
		}
		name := s.toks[i+1].text
		startTok := i
		if i > 0 && s.toks[i-1].text == "async" {
			startTok = i - 1
		}
		defLine := s.toks[startTok].line
		if seenLine[defLine] {
			continue
		}
		paren := -1
		for j := i + 2; j < len(s.toks); j++ {
			if s.toks[j].text == "(" {
				paren = j
				break
			}
			if s.toks[j].text == ":" && s.toks[j].line == t.line {
				break
			}
		}
		if paren < 0 || s.match[paren] < 0 {
			continue
		}
		closeParen := s.match[paren]
		colon := -1
		for j := closeParen + 1; j < len(s.toks); j++ {
			if s.toks[j].text == ":" {
				colon = j
				break
			}
			if s.toks[j].text == ";" || s.toks[j].text == "{" {
				break
			}
		}
		if colon < 0 {
			continue
		}
		seenLine[defLine] = true
		defIndent := s.lineIndent(defLine)
		colonLine := s.toks[colon].line
		colonPos := s.toks[colon].pos

		inline := false
		for j := colon + 1; j < len(s.toks) && s.toks[j].line == colonLine; j++ {
			inline = true
			break
		}

		bodyLineStart, bodyLineEnd := colonLine, colonLine
		if !inline {
			for ln := colonLine + 1; ln <= n; ln++ {
				if strings.TrimSpace(s.lineString(ln)) == "" {
					continue
				}
				if s.lineIndent(ln) > defIndent {
					bodyLineStart = ln
				}
				break
			}
			if bodyLineStart > colonLine {
				bodyLineEnd = bodyLineStart
				for ln := bodyLineStart + 1; ln <= n; ln++ {
					if strings.TrimSpace(s.lineString(ln)) == "" {
						continue
					}
					if s.lineIndent(ln) <= defIndent {
						break
					}
					bodyLineEnd = ln
				}
			} else {
				bodyLineStart = colonLine
			}
		}
		bodyStartLine := bodyLineStart
		if bodyStartLine < colonLine {
			bodyStartLine = colonLine
		}
		hasBody := inline || bodyLineEnd > colonLine
		bodyLo, bodyHi := -1, -1
		if hasBody {
			bodyLo, bodyHi = tokenRangeForLines(s, bodyStartLine, bodyLineEnd)
		}

		recv, recvIndent := "", -1
		for _, c := range classes {
			if c.line < defLine && defLine <= c.endLine && defIndent > c.indent && c.indent >= recvIndent {
				recv = c.name
				recvIndent = c.indent
			}
		}

		f := &genericFunc{name: name, recv: recv}
		f.key = name
		if recv != "" {
			f.key = recv + "." + name
		}
		kind := "function"
		if recv != "" {
			kind = "method"
		}
		startLine := s.toks[startTok].line
		f.fn = Function{
			Name:       name,
			Receiver:   recv,
			Kind:       kind,
			Signature:  signatureFromSource(s.src, s.toks[startTok].pos, colonPos+1),
			StartLine:  startLine,
			EndLine:    bodyLineEnd,
			StartChar:  s.toks[startTok].col,
			EndChar:    lineEndCol(s, bodyLineEnd),
			Exported:   !strings.HasPrefix(name, "_"),
			Complexity: 1,
		}
		if bodyLineStart <= bodyLineEnd {
			bodyBeginOffset, _ := s.lineBounds(bodyLineStart)
			_, bodyEndOffset := s.lineBounds(bodyLineEnd)
			if inline {
				bodyBeginOffset = colonPos + 1
			}
			if bodyBeginOffset < colonPos+1 && !inline {
				bodyBeginOffset = colonPos + 1
			}
			f.bodyStart = bodyBeginOffset
			f.bodyEnd = bodyEndOffset
		} else {
			f.bodyStart = colonPos + 1
			f.bodyEnd = colonPos + 1
		}
		f.bodyLo, f.bodyHi = bodyLo, bodyHi
		f.lineLo, f.lineHi = bodyLineStart, bodyLineEnd
		f.python = true
		if bodyLo >= 0 {
			f.fn.Complexity = pyComplexity(s, bodyLo, bodyHi)
		}
		out = append(out, f)
	}
	return out
}

func lineEndCol(s *genSource, ln int) int {
	text := strings.TrimRight(s.lineString(ln), " \t\r")
	return len(text)
}

func tokenRangeForLines(s *genSource, lo, hi int) (int, int) {
	if len(s.toks) == 0 {
		return -1, -1
	}
	first := sort.Search(len(s.toks), func(i int) bool { return s.toks[i].line >= lo })
	last := sort.Search(len(s.toks), func(i int) bool { return s.toks[i].line > hi })
	if first >= last {
		return -1, -1
	}
	return first, last - 1
}

func pyComplexity(s *genSource, lo, hi int) int {
	cc := 1
	for i := lo; i <= hi && i < len(s.toks); i++ {
		switch s.toks[i].text {
		case "if", "elif", "for", "while", "and", "or", "except", "case":
			if s.toks[i].text == "for" && i > lo && s.toks[i-1].text == "async" {
				continue
			}
			cc++
		}
	}
	return cc
}

func extractPyMembers(s *genSource, c *pyClass) {
	ti := c.ti
	// Class-level fields: the first indented, non-decorator line sets the
	// member indentation.
	memberIndent := -1
	for ln := c.line + 1; ln <= c.endLine; ln++ {
		mt := strings.TrimSpace(s.maskLineString(ln))
		if mt == "" {
			continue
		}
		ind := s.lineIndent(ln)
		if ind <= c.indent {
			break
		}
		if memberIndent < 0 {
			memberIndent = ind
		}
		if ind != memberIndent {
			continue
		}
		if strings.HasPrefix(mt, "@") || strings.HasPrefix(mt, "def ") ||
			strings.HasPrefix(mt, "async def ") || strings.HasPrefix(mt, "class ") ||
			strings.HasPrefix(mt, "import ") || strings.HasPrefix(mt, "from ") ||
			strings.HasPrefix(mt, "return") || strings.HasPrefix(mt, "pass") {
			continue
		}
		if name, typ, ok := pyAssignmentField(mt); ok {
			addField(ti, Field{Name: name, Type: typ})
		}
	}
	// Instance fields: self.name = ... / self.name: Type = ...
	for i := 0; i+3 < len(s.toks); i++ {
		if s.toks[i].line < c.line || s.toks[i].line > c.endLine {
			continue
		}
		if s.toks[i].text != "self" || s.toks[i+1].text != "." || !isIdentText(s.toks[i+2].text) {
			continue
		}
		if s.toks[i+3].text != "=" && s.toks[i+3].text != ":" && s.toks[i+3].text != "," {
			continue
		}
		typ := ""
		if s.toks[i+3].text == ":" {
			var parts []genToken
			for j := i + 4; j < len(s.toks) && s.toks[j].line == s.toks[i].line; j++ {
				if s.toks[j].text == "=" {
					break
				}
				parts = append(parts, s.toks[j])
			}
			typ = tokensText(parts)
		}
		addField(ti, Field{Name: s.toks[i+2].text, Type: typ})
	}
}

func pyAssignmentField(line string) (string, string, bool) {
	name := line
	typ := ""
	if idx := strings.IndexAny(name, ":="); idx >= 0 {
		if name[idx] == ':' {
			typ = strings.TrimSpace(name[idx+1:])
			if eq := strings.Index(typ, "="); eq >= 0 {
				typ = strings.TrimSpace(typ[:eq])
			}
		}
		name = strings.TrimSpace(name[:idx])
	}
	if !isIdentText(name) {
		return "", "", false
	}
	switch name {
	case "if", "for", "while", "with", "try", "else", "elif", "except",
		"return", "yield", "raise", "pass", "break", "continue", "del", "import":
		return "", "", false
	}
	return name, typ, true
}

func pyImports(s *genSource) []string {
	var out []string
	for ln := 1; ln <= s.lineCount(); ln++ {
		t := strings.TrimSpace(s.maskLineString(ln))
		switch {
		case strings.HasPrefix(t, "import "):
			rest := strings.TrimSpace(t[len("import "):])
			for _, item := range strings.Split(rest, ",") {
				item = strings.TrimSpace(item)
				if idx := strings.Index(item, " as "); idx >= 0 {
					item = strings.TrimSpace(item[:idx])
				}
				if item != "" && item != "(" && item != ")" {
					out = append(out, item)
				}
			}
		case strings.HasPrefix(t, "from "):
			rest := strings.TrimSpace(t[len("from "):])
			idx := strings.Index(rest, " import")
			if idx < 0 {
				continue
			}
			mod := strings.TrimSpace(rest[:idx])
			if mod == "" {
				mod = "."
			}
			out = append(out, mod)
		}
	}
	return out
}

// ---------------- brace languages ----------------

type braceCtx struct {
	ti         *TypeInfo
	name       string
	kind       string
	braceTok   int
	bodyLo     int
	bodyHi     int
	headerLo   int
	isImpl     bool
	implTarget string
	implTrait  string
}

func parseBraceLanguage(a *Analysis, src []byte, fam genFamily, note string) {
	style := lexC
	switch fam {
	case famJS:
		style = lexJS
	case famRust:
		style = lexRust
	}
	s := newGenSource(a.Path, a.Language, src, style)
	typesCtx, impls := collectBraceTypes(s, fam)
	funcs := collectBraceFunctions(s, fam, typesCtx, impls)
	for _, c := range typesCtx {
		if c.ti != nil && c.bodyLo <= c.bodyHi && c.ti.Name != "" {
			extractTypeMembers(s, fam, c)
		}
	}
	types := make([]*TypeInfo, 0, len(typesCtx))
	seen := make(map[string]bool)
	for _, c := range typesCtx {
		if c.ti == nil || c.ti.Name == "" || seen[c.ti.Name] {
			continue
		}
		seen[c.ti.Name] = true
		types = append(types, c.ti)
	}
	for _, t := range collectAliases(s, fam) {
		if t.Name == "" || seen[t.Name] {
			continue
		}
		seen[t.Name] = true
		types = append(types, t)
	}
	if fam == famRust {
		for _, impl := range impls {
			if impl.implTrait == "" {
				continue
			}
			for _, t := range types {
				if t.Name == impl.implTarget {
					t.Implements = append(t.Implements, impl.implTrait)
				}
			}
		}
		for _, t := range types {
			if len(t.Implements) > 1 {
				sort.Strings(t.Implements)
				t.Implements = dedupeSorted(t.Implements)
			}
		}
	}
	imports := braceImports(s, fam)
	engine := newGenericBigO(s, fam, funcs)
	finishGeneric(a, s, funcs, types, imports, engine)
	a.Notes = append(a.Notes, note,
		"Big-O is a static estimate from loop nesting, recursion shapes, known library costs and calls to functions defined in this file; runtime data can differ.")
	if warn := structuralWarning(s); warn != "" {
		a.Notes = append(a.Notes, warn)
	}
}

func structuralWarning(s *genSource) string {
	braces, parens, brackets := 0, 0, 0
	for _, t := range s.toks {
		switch t.text {
		case "{":
			braces++
		case "}":
			braces--
		case "(":
			parens++
		case ")":
			parens--
		case "[":
			brackets++
		case "]":
			brackets--
		}
	}
	if braces != 0 || parens != 0 || brackets != 0 {
		return "unbalanced brackets detected; the outline may be incomplete."
	}
	return ""
}

// headerBefore returns the token range of the statement header that ends just
// before idx, respecting parentheses so destructuring and multi-line calls do
// not truncate the header.
func (s *genSource) headerBefore(idx int) (int, int) {
	if idx <= 0 {
		return 0, idx
	}
	d := s.depth[idx]
	paren := 0
	lo := 0
	for j := idx - 1; j >= 0; j-- {
		t := s.toks[j]
		if s.directives && t.text == "#" {
			k := j
			for k+1 < idx && s.toks[k+1].line == t.line {
				k++
			}
			return k + 1, idx
		}
		switch t.text {
		case ")":
			paren++
		case "(":
			if paren > 0 {
				paren--
			}
		}
		if paren == 0 && (t.text == ";" || t.text == "{" || t.text == "}") && s.depthEnd[j] == d {
			lo = j + 1
			break
		}
	}
	return lo, idx
}

func collectBraceTypes(s *genSource, fam genFamily) ([]*braceCtx, []*braceCtx) {
	var types []*braceCtx
	var impls []*braceCtx
	for i, t := range s.toks {
		if t.text != "{" {
			continue
		}
		if s.parenDepth[i] > 0 {
			continue
		}
		lo, _ := s.headerBefore(i)
		if lo >= i {
			continue
		}
		if fam == famRust {
			if ctx, ok := parseImplHeader(s, lo, i); ok {
				bodyLo, bodyHi := i+1, len(s.toks)-1
				if m := s.match[i]; m >= 0 {
					bodyHi = m - 1
				}
				ctx.braceTok = i
				ctx.bodyLo, ctx.bodyHi = bodyLo, bodyHi
				impls = append(impls, ctx)
				continue
			}
		}
		hdr, ok := parseTypeHeader(s, fam, lo, i)
		if !ok {
			continue
		}
		m := s.match[i]
		bodyHi := len(s.toks) - 1
		endLine := s.lineOf(len(s.src) - 1)
		if m >= 0 {
			bodyHi = m - 1
			endLine = s.toks[m].line
		}
		if hdr.name == "" && fam == famC {
			if headerHas(s, lo, i, "typedef") {
				hdr.name = cTypedefName(s, m)
			}
		}
		if hdr.name == "" {
			continue
		}
		ti := &TypeInfo{
			Name:       hdr.name,
			Kind:       hdr.kind,
			StartLine:  hdr.startLine,
			EndLine:    endLine,
			Bases:      hdr.bases,
			Implements: hdr.implements,
			Embedded:   hdr.embedded,
			Exported:   hdr.exported,
			Fields:     hdr.fields,
		}
		types = append(types, &braceCtx{
			ti: ti, name: hdr.name, kind: hdr.kind, braceTok: i,
			bodyLo: i + 1, bodyHi: bodyHi, headerLo: lo,
		})
	}
	return types, impls
}

func cTypedefName(s *genSource, braceMatch int) string {
	if braceMatch < 0 {
		return ""
	}
	d := s.depthEnd[braceMatch]
	for j := braceMatch + 1; j < len(s.toks); j++ {
		if s.toks[j].text == ";" && s.depth[j] == d {
			break
		}
		if isIdentText(s.toks[j].text) {
			return s.toks[j].text
		}
		if s.toks[j].text == "{" || s.toks[j].text == "}" {
			break
		}
	}
	return ""
}

type braceTypeHeader struct {
	name       string
	kind       string
	startLine  int
	bases      []string
	implements []string
	embedded   []string
	fields     []Field
	exported   bool
}

func parseTypeHeader(s *genSource, fam genFamily, lo, hi int) (braceTypeHeader, bool) {
	var out braceTypeHeader
	keywords := map[string]bool{}
	switch fam {
	case famJS:
		keywords = map[string]bool{"class": true, "interface": true, "enum": true}
	case famRust:
		keywords = map[string]bool{"struct": true, "enum": true, "trait": true, "union": true}
	case famC:
		keywords = map[string]bool{"struct": true, "class": true, "union": true, "enum": true}
	case famJava:
		keywords = map[string]bool{"class": true, "interface": true, "enum": true, "record": true}
	}
	parenDepth := 0
	angleDepth := 0
	kw := -1
	for i := lo; i < hi; i++ {
		t := s.toks[i]
		switch t.text {
		case "(":
			parenDepth++
		case ")":
			if parenDepth > 0 {
				parenDepth--
			}
		case "<":
			if parenDepth == 0 {
				angleDepth++
			}
		case ">":
			if angleDepth > 0 {
				angleDepth--
			}
		case ">>":
			if angleDepth >= 2 {
				angleDepth -= 2
			} else if angleDepth == 1 {
				angleDepth = 0
			}
		case ">>>":
			angleDepth = 0
		}
		if parenDepth != 0 || angleDepth != 0 {
			continue
		}
		if keywords[t.text] {
			kw = i
			break
		}
	}
	if kw < 0 {
		return out, false
	}
	kwText := s.toks[kw].text
	nameIdx := -1
	for i := kw + 1; i < hi; i++ {
		t := s.toks[i]
		if t.text == "class" || t.text == "struct" || t.text == "final" ||
			t.text == "abstract" || t.text == "sealed" || t.text == "public" ||
			t.text == "extends" || t.text == "implements" {
			continue
		}
		if isIdentText(t.text) {
			nameIdx = i
			break
		}
		if t.text != "<" && t.text != ">" && t.text != "," && t.text != ":" {
			break
		}
	}
	kind := kwText
	if kind == "record" {
		kind = "class"
	}
	if kwText == "union" {
		kind = "struct"
	}
	if kwText == "trait" {
		kind = "trait"
	}
	out.kind = kind
	out.startLine = s.toks[kw].line
	if nameIdx >= 0 {
		out.name = s.toks[nameIdx].text
	}
	if fam == famJava && kwText == "record" && nameIdx >= 0 {
		out.fields = recordFields(s.toks[nameIdx+1 : hi])
	}
	// Reject struct-valued function definitions such as
	// `struct Point makePoint() {`, which share the keyword but are calls.
	if nameIdx >= 0 {
		for i := nameIdx + 1; i < hi; i++ {
			t := s.toks[i]
			if t.text == "(" {
				if fam == famC || fam == famRust {
					return out, false
				}
			}
			if t.text == "extends" || t.text == "implements" || t.text == ":" ||
				t.text == "where" || t.text == "for" {
				break
			}
		}
	}
	out.bases, out.implements = typeBases(s, fam, nameIdx, hi)
	switch fam {
	case famRust:
		out.exported = headerHas(s, lo, hi, "pub")
	case famJS:
		out.exported = headerHas(s, lo, hi, "export")
	case famJava:
		out.exported = headerHas(s, lo, hi, "public")
	case famC:
		out.exported = true
	}
	return out, true
}

func headerHas(s *genSource, lo, hi int, word string) bool {
	for i := lo; i < hi && i < len(s.toks); i++ {
		if s.toks[i].text == word {
			return true
		}
	}
	return false
}

func typeBases(s *genSource, fam genFamily, nameIdx, hi int) ([]string, []string) {
	var bases, implements []string
	if nameIdx < 0 {
		return nil, nil
	}
	parenDepth, angle := 0, 0
	mode := ""
	stopped := false
	start := -1
	flush := func(end int) {
		if start < 0 || end <= start {
			return
		}
		names := collectTypeNames(s.toks[start:end])
		switch mode {
		case "implements":
			implements = append(implements, names...)
		default:
			bases = append(bases, names...)
		}
		start = -1
	}
	for i := nameIdx + 1; i < hi; i++ {
		t := s.toks[i]
		switch t.text {
		case "(":
			parenDepth++
		case ")":
			if parenDepth > 0 {
				parenDepth--
			}
		case "<":
			angle++
		case ">":
			if angle > 0 {
				angle--
			}
		case ">>":
			if angle >= 2 {
				angle -= 2
			} else if angle == 1 {
				angle = 0
			}
		case ">>>":
			angle = 0
		}
		if parenDepth != 0 || angle != 0 {
			continue
		}
		switch {
		case t.text == "where" || t.text == "for":
			flush(i)
			mode = ""
			stopped = true
		case stopped:
		case t.text == "extends" || t.text == "implements":
			flush(i)
			mode = t.text
			start = i + 1
		case t.text == ":" && fam != famJS:
			flush(i)
			mode = "bases"
			start = i + 1
		}
	}
	flush(hi)
	return bases, implements
}

func collectTypeNames(ts []genToken) []string {
	var out []string
	angle, paren := 0, 0
	for i := 0; i < len(ts); i++ {
		t := ts[i]
		switch t.text {
		case "<":
			angle++
		case ">":
			if angle > 0 {
				angle--
			}
		case ">>":
			if angle >= 2 {
				angle -= 2
			} else if angle == 1 {
				angle = 0
			}
		case ">>>":
			angle = 0
		case "(":
			paren++
		case ")":
			if paren > 0 {
				paren--
			}
		}
		if angle != 0 || paren != 0 {
			continue
		}
		if !isIdentText(t.text) {
			continue
		}
		switch t.text {
		case "public", "private", "protected", "virtual", "final", "sealed",
			"abstract", "where", "for", "impl", "pub", "crate", "self", "super",
			"dyn", "static", "unsafe", "async", "default":
			continue
		}
		name := t.text
		for i+2 < len(ts) && ts[i+1].text == "::" && isIdentText(ts[i+2].text) {
			name += "::" + ts[i+2].text
			i += 2
		}
		out = append(out, name)
	}
	return out
}

func parseImplHeader(s *genSource, lo, hi int) (*braceCtx, bool) {
	impl := -1
	for i := lo; i < hi; i++ {
		if s.toks[i].text == "impl" {
			impl = i
			break
		}
		if s.toks[i].text == "fn" || s.toks[i].text == "struct" || s.toks[i].text == "enum" {
			return nil, false
		}
	}
	if impl < 0 {
		return nil, false
	}
	forTok := -1
	limit := hi
	for i := impl + 1; i < limit; i++ {
		if s.toks[i].text == "for" {
			forTok = i
			break
		}
		if s.toks[i].text == "where" {
			limit = i
			break
		}
	}
	trait := ""
	target := ""
	if forTok >= 0 {
		trait = lastTypeName(collectTypeNames(s.toks[impl+1 : forTok]))
		target = lastTypeName(collectTypeNames(s.toks[forTok+1 : limit]))
	} else {
		target = lastTypeName(collectTypeNames(s.toks[impl+1 : limit]))
	}
	if target == "" {
		return nil, false
	}
	return &braceCtx{isImpl: true, implTarget: target, implTrait: trait, headerLo: lo}, true
}

func lastTypeName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[len(names)-1]
}

func collectBraceFunctions(s *genSource, fam genFamily, types, impls []*braceCtx) []*genericFunc {
	claimed := make(map[int]bool)
	for _, c := range types {
		claimed[c.braceTok] = true
	}
	for _, c := range impls {
		claimed[c.braceTok] = true
	}
	var out []*genericFunc
	for i, t := range s.toks {
		if t.text != "{" || claimed[i] {
			continue
		}
		lo, hi := s.headerBefore(i)
		if lo >= hi {
			continue
		}
		if fam == famC && headerHas(s, lo, hi, "#") {
			continue
		}
		hdr, ok := parseFuncHeader(s, fam, lo, hi)
		if !ok {
			continue
		}
		if s.parenDepth[i] > 0 && !(fam == famJS && hdr.kind == "arrow") {
			continue
		}
		encType, encImpl := enclosingContexts(i, types, impls)
		recv := hdr.recv
		if recv == "" {
			if fam == famRust && encImpl != nil {
				recv = encImpl.implTarget
			} else if encType != nil {
				recv = encType.name
			}
		}
		kind := "function"
		if hdr.kind == "arrow" {
			kind = "arrow"
		} else if recv != "" {
			kind = "method"
		}
		m := s.match[i]
		bodyHi := len(s.toks) - 1
		endLine := s.lineOf(len(s.src) - 1)
		endChar := 0
		if m >= 0 {
			bodyHi = m - 1
			endLine = s.toks[m].line
			endChar = s.toks[m].col + 1
		} else if len(s.toks) > 0 {
			last := s.toks[len(s.toks)-1]
			endChar = last.col + len(last.text)
		}
		f := &genericFunc{name: hdr.name, recv: recv, bodyLo: i + 1, bodyHi: bodyHi}
		f.key = hdr.name
		if recv != "" {
			f.key = recv + "." + hdr.name
		}
		f.fn = Function{
			Name:       hdr.name,
			Receiver:   recv,
			Kind:       kind,
			Signature:  signatureFromSource(s.src, s.toks[lo].pos, t.pos),
			StartLine:  s.toks[lo].line,
			EndLine:    endLine,
			StartChar:  s.toks[lo].col,
			EndChar:    endChar,
			Exported:   funcExported(s, fam, lo, hi, recv, hdr.name, encType),
			Complexity: 1,
		}
		if i+1 < len(s.toks) {
			f.bodyStart = s.toks[i].pos + 1
		}
		if m >= 0 {
			f.bodyEnd = s.toks[m].pos
		} else {
			f.bodyEnd = len(s.src)
		}
		if f.bodyLo <= f.bodyHi {
			f.fn.Complexity = braceComplexity(s, fam, f.bodyLo, f.bodyHi)
		}
		out = append(out, f)
	}
	out = append(out, collectBraceArrowExpressions(s, fam, types, impls)...)
	out = append(out, collectBracePropertyArrows(s, fam, types, impls, out)...)
	return out
}

// collectBracePropertyArrows finds expression-bodied arrows used as object
// property values, e.g. `{ double: (x) => x * 2 }`.
func collectBracePropertyArrows(s *genSource, fam genFamily, types, impls []*braceCtx, existing []*genericFunc) []*genericFunc {
	if fam != famJS {
		return nil
	}
	known := make(map[string]bool, len(existing))
	for _, f := range existing {
		known[f.name+"\x00"+f.recv+"\x00"+itoa(f.fn.StartLine)] = true
	}
	var out []*genericFunc
	for arrow := 0; arrow < len(s.toks); arrow++ {
		if s.toks[arrow].text != "=>" {
			continue
		}
		if arrow+1 < len(s.toks) && s.toks[arrow+1].text == "{" {
			continue
		}
		lo, boundary := s.propertyStartBefore(arrow)
		if boundary < 0 || lo >= arrow {
			continue
		}
		bt := s.toks[boundary].text
		if bt != "{" && bt != "," {
			continue
		}
		slice := s.toks[lo:arrow]
		if topLevelToken(slice, 0, len(slice), "=") >= 0 {
			continue
		}
		if topLevelToken(slice, 0, len(slice), ":") < 0 {
			continue
		}
		encType, _ := enclosingContexts(arrow, types, impls)
		if encType != nil {
			if bt == "{" && boundary > 0 && s.toks[boundary-1].text != "=" &&
				s.toks[boundary-1].text != "(" && s.toks[boundary-1].text != "," &&
				s.toks[boundary-1].text != "[" && s.toks[boundary-1].text != "=>" &&
				s.toks[boundary-1].text != "return" {
				continue
			}
			if encType.kind == "interface" {
				continue
			}
		} else if bt == "{" && isTypeAliasObject(s, lo, boundary) {
			continue
		} else if bt == "{" && boundary >= 3 && s.toks[boundary-1].text == ":" &&
			isIdentText(s.toks[boundary-2].text) {
			switch s.toks[boundary-3].text {
			case "const", "let", "var":
				continue // `const x: { fn: (a) => T } = ...`
			}
		} else if bt == "," {
			eb := s.enclosingBraceFor(arrow, s.depth[arrow])
			if eb >= 0 && isTypeAliasObject(s, 0, eb) {
				continue
			}
			if eb >= 3 && s.toks[eb-1].text == ":" && isIdentText(s.toks[eb-2].text) {
				switch s.toks[eb-3].text {
				case "const", "let", "var", "(":
					continue
				}
			}
		}
		name := assignmentName(slice)
		if name == "" || genNonCallNames[name] || genBlockKeywords[name] {
			continue
		}
		bodyHi := s.arrowExprBodyEnd(arrow, len(s.toks)-1)
		if bodyHi <= arrow {
			continue
		}
		recv := ""
		if encType != nil {
			recv = encType.name
		}
		key := name + "\x00" + recv + "\x00" + itoa(s.toks[lo].line)
		if known[key] {
			continue
		}
		known[key] = true
		last := s.toks[bodyHi]
		f := &genericFunc{name: name, recv: recv, bodyLo: arrow + 1, bodyHi: bodyHi}
		f.key = name
		if recv != "" {
			f.key = recv + "." + name
		}
		f.fn = Function{
			Name:       name,
			Receiver:   recv,
			Kind:       "arrow",
			Signature:  signatureFromSource(s.src, s.toks[lo].pos, s.toks[arrow].pos+2),
			StartLine:  s.toks[lo].line,
			EndLine:    last.line,
			StartChar:  s.toks[lo].col,
			EndChar:    last.col + len(last.text),
			Exported:   funcExported(s, fam, lo, arrow, recv, name, encType),
			Complexity: 1,
		}
		f.bodyStart = s.toks[arrow].pos + 2
		f.bodyEnd = last.pos + len(last.text)
		if f.bodyLo <= f.bodyHi {
			f.fn.Complexity = braceComplexity(s, fam, f.bodyLo, f.bodyHi)
		}
		out = append(out, f)
	}
	return out
}

// propertyStartBefore finds the start of the current object member before an
// arrow, returning the boundary token index too.
func (s *genSource) propertyStartBefore(arrow int) (int, int) {
	d := s.depth[arrow]
	paren, bracket := 0, 0
	for j := arrow - 1; j >= 0; j-- {
		t := s.toks[j]
		switch t.text {
		case ")":
			paren++
		case "(":
			if paren > 0 {
				paren--
			}
		case "]":
			bracket++
		case "[":
			if bracket > 0 {
				bracket--
			}
		}
		if paren != 0 || bracket != 0 {
			continue
		}
		switch t.text {
		case "{", "}", ",", ";":
			if s.depthEnd[j] == d {
				return j + 1, j
			}
		}
	}
	return 0, -1
}

func (s *genSource) arrowExprBodyEnd(arrow, limit int) int {
	d := s.depth[arrow]
	paren, bracket, brace := 0, 0, 0
	for j := arrow + 1; j <= limit && j < len(s.toks); j++ {
		t := s.toks[j]
		switch t.text {
		case "(":
			paren++
		case ")":
			if paren > 0 {
				paren--
			}
		case "[":
			bracket++
		case "]":
			if bracket > 0 {
				bracket--
			}
		case "{":
			brace++
		case "}":
			if brace > 0 {
				brace--
			} else if paren == 0 && bracket == 0 {
				return j - 1
			}
		case ",", ";":
			if paren == 0 && bracket == 0 && brace == 0 && s.depth[j] == d {
				return j - 1
			}
		}
	}
	return limit
}

func (s *genSource) enclosingBraceFor(tok, depth int) int {
	for j := tok - 1; j >= 0; j-- {
		if s.toks[j].text == "{" && s.depth[j] == depth-1 && s.depthEnd[j] == depth {
			return j
		}
	}
	return -1
}

func isTypeAliasObject(s *genSource, lo, boundary int) bool {
	if boundary <= 0 {
		return false
	}
	start := 0
	d := s.depth[boundary]
	for j := boundary - 1; j >= 0; j-- {
		t := s.toks[j]
		if (t.text == ";" || t.text == "{" || t.text == "}") && s.depthEnd[j] == d {
			start = j + 1
			break
		}
	}
	_ = lo
	for i := start; i < boundary; i++ {
		switch s.toks[i].text {
		case "type", "interface":
			return true
		case "const", "let", "var", "return", "=":
			return false
		}
	}
	return false
}

func enclosingContexts(braceTok int, types, impls []*braceCtx) (*braceCtx, *braceCtx) {
	var encType, encImpl *braceCtx
	typeSpan := -1
	for _, c := range types {
		if c.bodyLo <= braceTok && braceTok <= c.bodyHi {
			span := c.bodyHi - c.bodyLo
			if typeSpan < 0 || span < typeSpan {
				typeSpan = span
				encType = c
			}
		}
	}
	implSpan := -1
	for _, c := range impls {
		if c.bodyLo <= braceTok && braceTok <= c.bodyHi {
			span := c.bodyHi - c.bodyLo
			if implSpan < 0 || span < implSpan {
				implSpan = span
				encImpl = c
			}
		}
	}
	return encType, encImpl
}

func funcExported(s *genSource, fam genFamily, lo, hi int, recv, name string, encType *braceCtx) bool {
	switch fam {
	case famRust:
		if encType != nil && encType.kind == "trait" {
			return true
		}
		return headerHas(s, lo, hi, "pub")
	case famJS:
		if headerHas(s, lo, hi, "export") {
			return true
		}
		if encType != nil {
			return !strings.HasPrefix(name, "#") && !strings.HasPrefix(name, "_")
		}
		return false
	case famJava:
		if encType != nil && encType.kind == "interface" {
			return true
		}
		return headerHas(s, lo, hi, "public")
	case famC:
		return !headerHas(s, lo, hi, "static")
	}
	return false
}

type braceFuncHeader struct {
	name string
	recv string
	kind string
}

var genBlockKeywords = map[string]bool{
	"if": true, "else": true, "for": true, "while": true, "do": true,
	"switch": true, "case": true, "default": true, "catch": true,
	"finally": true, "try": true, "return": true, "throw": true,
	"new": true, "delete": true, "assert": true, "static_assert": true,
	"using": true, "synchronized": true, "lock": true, "foreach": true,
	"match": true, "loop": true, "impl": true, "namespace": true,
	"unless": true, "until": true, "with": true,
	"when": true, "expect": true, "typedef": true,
}

var genNonCallNames = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"return": true, "sizeof": true, "decltype": true, "alignof": true,
	"alignas": true, "typeof": true, "new": true, "delete": true,
	"throw": true, "case": true, "do": true, "else": true, "try": true,
	"finally": true, "using": true, "synchronized": true, "lock": true,
	"foreach": true, "await": true, "yield": true, "assert": true,
	"static_assert": true, "when": true, "unless": true, "until": true,
	"with": true, "match": true, "loop": true, "function": true,
	"def": true, "fn": true, "class": true, "struct": true, "enum": true,
	"interface": true, "trait": true, "union": true, "impl": true,
	"namespace": true, "import": true, "package": true, "let": true,
	"const": true, "var": true, "static": true, "public": true,
	"private": true, "protected": true,
}

func parseFuncHeader(s *genSource, fam genFamily, lo, hi int) (braceFuncHeader, bool) {
	var out braceFuncHeader
	if lo >= hi {
		return out, false
	}
	if fam == famRust {
		fn := -1
		for i := lo; i < hi; i++ {
			if s.toks[i].text == "fn" {
				fn = i
				break
			}
			if s.toks[i].text == "impl" || s.toks[i].text == "struct" ||
				s.toks[i].text == "enum" || s.toks[i].text == "trait" ||
				s.toks[i].text == "use" || s.toks[i].text == "let" {
				return out, false
			}
		}
		if fn < 0 {
			return out, false
		}
		if fn+1 >= hi || !isIdentText(s.toks[fn+1].text) {
			return out, false
		}
		if s.toks[fn+1].text == "where" {
			return out, false
		}
		hasParams := false
		for i := fn + 2; i < hi; i++ {
			if s.toks[i].text == "(" {
				hasParams = true
				break
			}
		}
		if !hasParams {
			return out, false
		}
		out.name = s.toks[fn+1].text
		out.kind = "function"
		return out, true
	}
	if fam == famJS {
		fn := -1
		for i := lo; i < hi; i++ {
			if s.toks[i].text == "function" {
				fn = i
				break
			}
		}
		if fn >= 0 {
			name := ""
			for i := fn + 1; i < hi; i++ {
				if s.toks[i].text == "*" {
					continue
				}
				if isIdentText(s.toks[i].text) {
					name = s.toks[i].text
				}
				break
			}
			if name == "" {
				name = assignmentName(s.toks[lo:fn])
			}
			if name == "" || genBlockKeywords[name] || genNonCallNames[name] {
				return out, false
			}
			out.name = name
			out.kind = "function"
			return out, true
		}
		if arrow := lastTopLevelToken(s.toks, lo, hi, "=>"); arrow >= 0 && hi-1 == arrow {
			name := arrowName(s.toks[lo:arrow])
			if name == "" {
				return out, false
			}
			out.name = name
			out.kind = "arrow"
			return out, true
		}
	}
	// C-like and Java paren-based header. Choose the first plausible
	// top-level parameter list, skipping annotations, attribute arguments and
	// constructor initializer calls.
	paren := -1
	parenDepth := 0
	for i := lo; i < hi; i++ {
		t := s.toks[i]
		if t.text == "(" {
			if parenDepth == 0 && paren < 0 {
				if i > lo {
					cand := i - 1
					name := ""
					if isIdentText(s.toks[cand].text) {
						name = s.toks[cand].text
					} else if (s.toks[cand].text == "?" || s.toks[cand].text == "!") &&
						cand-1 >= lo && isIdentText(s.toks[cand-1].text) {
						name = s.toks[cand-1].text
						cand--
					} else if s.toks[cand].text == "~" && cand-1 >= lo && isIdentText(s.toks[cand-1].text) {
						name = "~" + s.toks[cand-1].text
						cand--
					}
					skip := name == ""
					if name != "" && (genBlockKeywords[name] || genNonCallNames[name]) {
						skip = true
					}
					if i >= 2 {
						switch s.toks[i-2].text {
						case "@", ":", ",":
							skip = true
						}
					}
					if i+1 < hi && (s.toks[i+1].text == "*" || s.toks[i+1].text == "&") {
						skip = true
					}
					if !skip && headerHas(s, lo, i, "new") {
						skip = true
					}
					if !skip {
						paren = i
					}
				}
			}
			parenDepth++
		} else if t.text == ")" {
			if parenDepth > 0 {
				parenDepth--
			}
		}
		if paren >= 0 {
			break
		}
	}
	if paren < 1 {
		return out, false
	}
	prev := paren - 1
	name := ""
	if isIdentText(s.toks[prev].text) {
		name = s.toks[prev].text
		if prev-1 >= lo && s.toks[prev-1].text == "~" {
			name = "~" + name
			prev--
		}
	} else if (s.toks[prev].text == "?" || s.toks[prev].text == "!") &&
		prev-1 >= lo && isIdentText(s.toks[prev-1].text) {
		name = s.toks[prev-1].text
		prev--
	} else if s.toks[prev].text == "~" && prev-1 >= lo && isIdentText(s.toks[prev-1].text) {
		name = "~" + s.toks[prev-1].text
		prev--
	}
	if name == "" || genBlockKeywords[name] || genNonCallNames[name] {
		return out, false
	}
	close := s.match[paren]
	if close < 0 || close >= hi {
		return out, false
	}
	angle := 0
	for i := lo; i < paren; i++ {
		switch s.toks[i].text {
		case "<":
			angle++
		case ">":
			if angle > 0 {
				angle--
			}
		}
		if angle != 0 {
			continue
		}
		if s.toks[i].text == "=" && !(i > lo && s.toks[i-1].text == "operator") {
			return out, false
		}
	}
	if fam == famJS {
		if !jsTypeTailOK(s.toks, close+1, hi) {
			return out, false
		}
	}
	if prev >= 2 && s.toks[prev-1].text == "::" && isIdentText(s.toks[prev-2].text) {
		out.recv = s.toks[prev-2].text
	}
	out.name = name
	out.kind = "function"
	return out, true
}

func jsTypeTailOK(ts []genToken, from, hi int) bool {
	for i := from; i < hi && i < len(ts); i++ {
		switch ts[i].text {
		case "=", ";", "{", "}":
			return false
		}
	}
	return true
}

func lastTopLevelToken(ts []genToken, lo, hi int, text string) int {
	paren, bracket := 0, 0
	found := -1
	for i := lo; i < hi; i++ {
		switch ts[i].text {
		case "(":
			paren++
		case ")":
			if paren > 0 {
				paren--
			}
		case "[":
			bracket++
		case "]":
			if bracket > 0 {
				bracket--
			}
		}
		if paren == 0 && bracket == 0 && ts[i].text == text {
			found = i
		}
	}
	return found
}

func topLevelToken(ts []genToken, lo, hi int, text string) int {
	paren, bracket := 0, 0
	for i := lo; i < hi; i++ {
		switch ts[i].text {
		case "(":
			paren++
		case ")":
			if paren > 0 {
				paren--
			}
		case "[":
			bracket++
		case "]":
			if bracket > 0 {
				bracket--
			}
		}
		if paren == 0 && bracket == 0 && ts[i].text == text {
			return i
		}
	}
	return -1
}

func assignmentName(ts []genToken) string {
	if len(ts) == 0 {
		return ""
	}
	if v := modifierAssignedName(ts); v != "" {
		return v
	}
	colon, eq := -1, -1
	paren, bracket := 0, 0
	for i, t := range ts {
		switch t.text {
		case "(":
			paren++
		case ")":
			if paren > 0 {
				paren--
			}
		case "[":
			bracket++
		case "]":
			if bracket > 0 {
				bracket--
			}
		}
		if paren != 0 || bracket != 0 {
			continue
		}
		if t.text == ":" && colon < 0 {
			colon = i
		}
		if t.text == "=" && eq < 0 {
			eq = i
		}
	}
	pick := -1
	if colon >= 0 && (eq < 0 || colon < eq) {
		pick = colon
	} else if eq >= 0 {
		pick = eq
	}
	if pick <= 0 {
		return ""
	}
	return lastIdentBefore(ts, pick)
}

func modifierAssignedName(ts []genToken) string {
	for i := 0; i+1 < len(ts); i++ {
		switch ts[i].text {
		case "const", "let", "var":
			if isIdentText(ts[i+1].text) {
				return ts[i+1].text
			}
		}
	}
	return ""
}

func lastIdentBefore(ts []genToken, idx int) string {
	for i := idx - 1; i >= 0; i-- {
		if isIdentText(ts[i].text) {
			switch ts[i].text {
			case "public", "private", "protected", "static", "readonly",
				"declare", "abstract", "async", "const", "let", "var", "export":
				continue
			}
			return ts[i].text
		}
	}
	return ""
}

func arrowName(ts []genToken) string {
	if v := modifierAssignedName(ts); v != "" {
		return v
	}
	if v := assignmentName(ts); v != "" {
		return v
	}
	return ""
}

func collectBraceArrowExpressions(s *genSource, fam genFamily, types, impls []*braceCtx) []*genericFunc {
	if fam != famJS {
		return nil
	}
	var out []*genericFunc
	for i, t := range s.toks {
		if t.text != ";" {
			continue
		}
		lo, _ := s.headerBefore(i)
		if lo >= i {
			continue
		}
		if statementStartsWith(s.toks[lo:i], "type", "export", "declare") {
			// TS type aliases use `=>` in function types.
			continue
		}
		arrow := lastTopLevelToken(s.toks, lo, i, "=>")
		if arrow < 0 {
			continue
		}
		if arrow+1 < len(s.toks) && s.toks[arrow+1].text == "{" {
			continue // brace body already handled
		}
		name := arrowName(s.toks[lo:arrow])
		if name == "" {
			continue
		}
		encType, _ := enclosingContexts(i, types, impls)
		if encType != nil && topLevelToken(s.toks, lo, arrow, "=") < 0 {
			// A `name: (args) => Type;` declaration inside a class or
			// interface describes a field type, not a function body.
			continue
		}
		recv := ""
		if encType != nil {
			recv = encType.name
		}
		kind := "arrow"
		f := &genericFunc{name: name, recv: recv, bodyLo: arrow + 1, bodyHi: i - 1}
		f.key = name
		if recv != "" {
			f.key = recv + "." + name
		}
		f.fn = Function{
			Name:       name,
			Receiver:   recv,
			Kind:       kind,
			Signature:  signatureFromSource(s.src, s.toks[lo].pos, s.toks[arrow].pos+2),
			StartLine:  s.toks[lo].line,
			EndLine:    t.line,
			StartChar:  s.toks[lo].col,
			EndChar:    t.col,
			Exported:   funcExported(s, fam, lo, arrow, recv, name, encType),
			Complexity: 1,
		}
		if arrow+1 < len(s.toks) {
			f.bodyStart = s.toks[arrow].pos + 2
		}
		f.bodyEnd = t.pos
		if f.bodyLo <= f.bodyHi {
			f.fn.Complexity = braceComplexity(s, fam, f.bodyLo, f.bodyHi)
		}
		out = append(out, f)
	}
	return out
}

func statementStartsWith(ts []genToken, words ...string) bool {
	i := 0
	for i < len(ts) && (ts[i].text == "export" || ts[i].text == "declare" ||
		ts[i].text == "default" || ts[i].text == "async") {
		i++
	}
	if i >= len(ts) {
		return false
	}
	for _, w := range words {
		if ts[i].text == w {
			return true
		}
	}
	return false
}

func braceComplexity(s *genSource, fam genFamily, lo, hi int) int {
	cc := 1
	for i := lo; i <= hi && i < len(s.toks); i++ {
		switch s.toks[i].text {
		case "if", "for", "while", "case", "catch", "&&", "||", "??":
			cc++
		case "loop", "match":
			if fam == famRust {
				cc++
			}
		}
	}
	return cc
}

// ---------------- type members ----------------

func extractTypeMembers(s *genSource, fam genFamily, ctx *braceCtx) {
	ti := ctx.ti
	if ti == nil || ctx.bodyLo > ctx.bodyHi {
		return
	}
	directDepth := s.depth[ctx.bodyLo]
	lastLine := ti.EndLine
	enumConstsDone := false
	for ln := ti.StartLine; ln <= lastLine; ln++ {
		first := s.firstTokenOnLine(ln, ctx.bodyLo, ctx.bodyHi)
		if first < 0 {
			continue
		}
		if s.depth[first] != directDepth {
			continue
		}
		mt := strings.TrimSpace(s.maskLineString(ln))
		if mt == "" {
			continue
		}
		if isMemberNoise(mt) {
			continue
		}
		var seg []genToken
		hasOpenBrace := false
		for j := first; j <= ctx.bodyHi && j < len(s.toks); j++ {
			t := s.toks[j]
			if t.line != ln {
				break
			}
			if s.depth[j] < directDepth {
				break
			}
			if s.depth[j] > directDepth {
				hasOpenBrace = true
				break
			}
			if t.text == "{" {
				hasOpenBrace = true
				break
			}
			seg = append(seg, t)
		}
		if len(seg) == 0 {
			continue
		}
		if hasOpenBrace {
			// Method definition: function extraction owns it. Enum variants
			// with a payload block still expose their name.
			if ctx.kind == "enum" && !enumConstsDone && len(seg) > 0 && isIdentText(seg[0].text) {
				addField(ti, Field{Name: seg[0].text, Type: tokensText(seg[1:])})
			}
			continue
		}
		declEndsSemi := strings.HasSuffix(strings.TrimSpace(mt), ";")
		if ctx.kind == "enum" && !enumConstsDone {
			parseEnumVariants(ti, seg, fam)
			if declEndsSemi {
				enumConstsDone = true
			}
			continue
		}
		if name, ok := memberMethodName(seg, fam); ok {
			if declEndsSemi {
				sig := signatureFromSource(s.src, seg[0].pos, lineEndOffset(s, ln))
				addMethod(ti, Method{Name: name, Signature: sig})
				continue
			}
		}
		parseFieldSegments(s, fam, ctx, seg)
	}
}

func parseEnumVariants(ti *TypeInfo, seg []genToken, fam genFamily) {
	for len(seg) > 0 && (seg[len(seg)-1].text == ";" || seg[len(seg)-1].text == ",") {
		seg = seg[:len(seg)-1]
	}
	for _, part := range splitTopLevel(seg, ",") {
		part = stripMemberModifiers(part, fam)
		if len(part) == 0 || !isIdentText(part[0].text) {
			continue
		}
		addField(ti, Field{Name: part[0].text, Type: tokensText(part[1:])})
	}
}

func lineEndOffset(s *genSource, ln int) int {
	_, hi := s.lineBounds(ln)
	return hi
}

func isMemberNoise(mt string) bool {
	if mt == "" {
		return true
	}
	switch mt[0] {
	case '{', '}', ';', ',', ')', ']', ':', '*':
		return true
	}
	lower := strings.ToLower(mt)
	for _, prefix := range []string{
		"public:", "private:", "protected:", "public slots:", "private slots:",
		"signals:", "slots:", "case ", "default:", "else",
		"#", "[", "@", "using ", "typedef ", "namespace ", "package ",
		"import ", "return ", "if ", "for ", "while ", "switch ", "try ",
		"catch ", "finally", "do ", "throw ", "assert ", "static_assert",
		"break", "continue", "goto ", "delete ", "new ", "yield ", "await ",
		"static {", "friend ", "template ", "operator ",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if strings.HasSuffix(mt, ":") && !strings.ContainsAny(mt, "=;()") {
		return true
	}
	return false
}

func memberMethodName(seg []genToken, fam genFamily) (string, bool) {
	paren := -1
	parenDepth := 0
	for i, t := range seg {
		switch t.text {
		case "(":
			if parenDepth == 0 {
				paren = i
			}
			parenDepth++
		case ")":
			if parenDepth > 0 {
				parenDepth--
			}
		}
		if paren >= 0 {
			break
		}
	}
	if paren <= 0 {
		return "", false
	}
	for i := 0; i < paren; i++ {
		if seg[i].text == "=" {
			return "", false
		}
	}
	name := ""
	fnIdx := -1
	for i := 0; i < paren; i++ {
		if seg[i].text == "fn" {
			fnIdx = i
		}
	}
	if fnIdx >= 0 {
		for i := fnIdx + 1; i < paren; i++ {
			if isIdentText(seg[i].text) {
				name = seg[i].text
				break
			}
		}
	} else {
		prev := paren - 1
		if prev >= 0 && isIdentText(seg[prev].text) {
			name = seg[prev].text
			if prev >= 1 && seg[prev-1].text == "~" {
				name = "~" + name
			}
		} else if prev >= 1 && (seg[prev].text == "?" || seg[prev].text == "!") &&
			isIdentText(seg[prev-1].text) {
			name = seg[prev-1].text
		} else if prev >= 1 && seg[prev].text == "~" && isIdentText(seg[prev-1].text) {
			name = "~" + seg[prev-1].text
		}
	}
	if name == "" || genNonCallNames[name] || genBlockKeywords[name] {
		return "", false
	}
	return name, true
}

func parseFieldSegments(s *genSource, fam genFamily, ctx *braceCtx, seg []genToken) {
	parts := splitTopLevel(seg, ",")
	carriedType := ""
	for _, part := range parts {
		part = stripMemberModifiers(part, fam)
		if len(part) == 0 {
			continue
		}
		switch fam {
		case famRust:
			parseRustField(ctx, part)
		case famJS:
			parseJSField(ctx, part)
		case famJava, famC:
			f, ok := cFieldOf(part, fam, carriedType)
			if !ok {
				continue
			}
			if f.Type == "" {
				f.Type = carriedType
			}
			if f.Type != "" {
				carriedType = f.Type
			}
			addField(ctx.ti, f)
		}
	}
}

func splitTopLevel(ts []genToken, sep string) [][]genToken {
	var out [][]genToken
	paren, bracket, angle, brace := 0, 0, 0, 0
	start := 0
	for i, t := range ts {
		switch t.text {
		case "(":
			paren++
		case ")":
			if paren > 0 {
				paren--
			}
		case "[":
			bracket++
		case "]":
			if bracket > 0 {
				bracket--
			}
		case "<":
			angle++
		case ">":
			if angle > 0 {
				angle--
			}
		case ">>":
			if angle >= 2 {
				angle -= 2
			} else if angle == 1 {
				angle = 0
			}
		case ">>>":
			if angle >= 3 {
				angle -= 3
			} else {
				angle = 0
			}
		case "{":
			brace++
		case "}":
			if brace > 0 {
				brace--
			}
		}
		if paren == 0 && bracket == 0 && angle == 0 && brace == 0 && t.text == sep {
			out = append(out, ts[start:i])
			start = i + 1
		}
	}
	out = append(out, ts[start:])
	return out
}

var genMemberModifiers = map[string]bool{
	"public": true, "private": true, "protected": true, "static": true,
	"final": true, "readonly": true, "abstract": true, "override": true,
	"export": true, "declare": true, "default": true, "volatile": true,
	"transient": true, "synchronized": true, "native": true, "strictfp": true,
	"virtual": true, "inline": true, "explicit": true, "friend": true,
	"mutable": true, "constexpr": true, "consteval": true, "constinit": true,
	"pub": true, "unsafe": true, "async": true, "extern": true, "mut": true,
}

func stripMemberModifiers(ts []genToken, fam genFamily) []genToken {
	i := 0
	for i < len(ts) {
		t := ts[i].text
		if t == "@" || t == "#" && i+1 < len(ts) && ts[i+1].text == "[" {
			// Annotation or attribute.
			i++
			if t == "#" {
				if i < len(ts) {
					i++
				}
				depth := 1
				for i < len(ts) && depth > 0 {
					switch ts[i].text {
					case "[":
						depth++
					case "]":
						depth--
					}
					i++
				}
			} else if i < len(ts) && isIdentText(ts[i].text) {
				i++
			}
			continue
		}
		if t == "pub" && i+1 < len(ts) && ts[i+1].text == "(" {
			depth := 0
			i++
			for i < len(ts) {
				switch ts[i].text {
				case "(":
					depth++
				case ")":
					depth--
				}
				i++
				if depth == 0 {
					break
				}
			}
			continue
		}
		if genMemberModifiers[t] {
			i++
			continue
		}
		break
	}
	return ts[i:]
}

func parseRustField(ctx *braceCtx, ts []genToken) {
	if len(ts) == 0 {
		return
	}
	if ts[0].text == "fn" || ts[0].text == "type" || ts[0].text == "const" ||
		ts[0].text == "use" || ts[0].text == "impl" || ts[0].text == "pub" {
		return
	}
	if ctx.kind == "enum" {
		if !isIdentText(ts[0].text) {
			return
		}
		name := ts[0].text
		typ := ""
		if len(ts) > 1 {
			typ = tokensText(ts[1:])
		}
		addField(ctx.ti, Field{Name: name, Type: typ})
		return
	}
	colon := -1
	for i, t := range ts {
		if t.text == ":" {
			colon = i
			break
		}
	}
	if colon <= 0 {
		return
	}
	name := ""
	for i := colon - 1; i >= 0; i-- {
		if isIdentText(ts[i].text) {
			name = ts[i].text
			break
		}
	}
	if name == "" || !isIdentText(name) {
		return
	}
	typ := tokensText(ts[colon+1:])
	addField(ctx.ti, Field{Name: name, Type: typ})
}

func parseJSField(ctx *braceCtx, ts []genToken) {
	if len(ts) == 0 {
		return
	}
	name := ""
	start := 0
	if ts[0].text == "#" && len(ts) > 1 && isIdentText(ts[1].text) {
		name = "#" + ts[1].text
		start = 2
	} else if isIdentText(ts[0].text) {
		name = ts[0].text
		start = 1
	}
	if name == "" || genNonCallNames[name] || genBlockKeywords[name] {
		return
	}
	typ := ""
	for i := start; i < len(ts); i++ {
		if ts[i].text == "?" || ts[i].text == "!" {
			continue
		}
		if ts[i].text == ":" {
			var parts []genToken
			for j := i + 1; j < len(ts); j++ {
				if ts[j].text == "=" || ts[j].text == ";" {
					break
				}
				parts = append(parts, ts[j])
			}
			typ = tokensText(parts)
			break
		}
		if ts[i].text == "=" || ts[i].text == ";" {
			break
		}
	}
	addField(ctx.ti, Field{Name: name, Type: typ})
}

func cFieldOf(ts []genToken, fam genFamily, carried string) (Field, bool) {
	if len(ts) == 0 {
		return Field{}, false
	}
	for _, t := range ts {
		if t.text == "operator" {
			return Field{}, false
		}
	}
	eq := -1
	for i, t := range ts {
		if t.text == "=" {
			eq = i
			break
		}
	}
	decl := ts
	if eq >= 0 {
		decl = ts[:eq]
	}
	for len(decl) > 0 && (decl[len(decl)-1].text == ";" || decl[len(decl)-1].text == ":") {
		decl = decl[:len(decl)-1]
	}
	if len(decl) == 0 {
		return Field{}, false
	}
	nameIdx := -1
	for i := len(decl) - 1; i >= 0; i-- {
		if isIdentText(decl[i].text) {
			nameIdx = i
			break
		}
	}
	if nameIdx < 0 {
		return Field{}, false
	}
	name := decl[nameIdx].text
	if genNonCallNames[name] || genMemberModifiers[name] || genBlockKeywords[name] || genTypeWords[name] {
		return Field{}, false
	}
	typ := tokensText(decl[:nameIdx])
	if nameIdx == 0 {
		if carried == "" {
			return Field{}, false
		}
		typ = carried
	}
	return Field{Name: name, Type: typ}, true
}

var genTypeWords = map[string]bool{
	"int": true, "char": true, "void": true, "float": true, "double": true,
	"long": true, "short": true, "unsigned": true, "signed": true, "bool": true,
	"boolean": true, "byte": true, "const": true, "static": true, "final": true,
	"auto": true, "struct": true, "enum": true, "union": true, "class": true,
}

func addField(ti *TypeInfo, f Field) {
	if ti == nil || f.Name == "" {
		return
	}
	for _, have := range ti.Fields {
		if have.Name == f.Name {
			return
		}
	}
	ti.Fields = append(ti.Fields, f)
}

func addMethod(ti *TypeInfo, m Method) {
	if ti == nil || m.Name == "" {
		return
	}
	for i := range ti.Methods {
		if ti.Methods[i].Name == m.Name {
			if m.Signature != "" && (ti.Methods[i].Signature == "" ||
				strings.HasSuffix(strings.TrimSpace(ti.Methods[i].Signature), ";")) {
				ti.Methods[i].Signature = m.Signature
			}
			return
		}
	}
	ti.Methods = append(ti.Methods, m)
}

func tokensText(ts []genToken) string {
	var b strings.Builder
	prevIdent := false
	for _, t := range ts {
		cur := isIdentText(t.text) || isIntText(t.text)
		if b.Len() > 0 && prevIdent && cur {
			b.WriteByte(' ')
		}
		b.WriteString(t.text)
		prevIdent = cur
	}
	s := b.String()
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// ---------------- aliases ----------------

func collectAliases(s *genSource, fam genFamily) []*TypeInfo {
	var out []*TypeInfo
	for i, t := range s.toks {
		if t.text != ";" {
			continue
		}
		lo, _ := s.headerBefore(i)
		if lo >= i {
			continue
		}
		seg := s.toks[lo:i]
		startLine := seg[0].line
		switch fam {
		case famRust:
			if ti, ok := rustAlias(s, seg, startLine, t.line); ok {
				out = append(out, ti)
			}
		case famJS:
			if ti, ok := jsTypeAlias(s, seg, startLine, t.line); ok {
				out = append(out, ti)
			}
		case famC:
			if ti, ok := cAlias(s, seg, startLine, t.line); ok {
				out = append(out, ti)
			}
		}
	}
	return out
}

func rustAlias(s *genSource, seg []genToken, startLine, endLine int) (*TypeInfo, bool) {
	start := 0
	for start < len(seg) && (seg[start].text == "pub" || seg[start].text == "unsafe" || seg[start].text == "async") {
		start++
	}
	if start >= len(seg) {
		return nil, false
	}
	switch seg[start].text {
	case "struct":
		if start+1 >= len(seg) || !isIdentText(seg[start+1].text) {
			return nil, false
		}
		name := seg[start+1].text
		ti := &TypeInfo{Name: name, Kind: "struct", StartLine: startLine, EndLine: endLine,
			Exported: headerHas(s, 0, len(seg), "pub")}
		if start+2 < len(seg) && seg[start+2].text == "(" {
			params := tupleFields(seg[start+3:], true)
			ti.Fields = params
		}
		return ti, true
	case "type":
		if start+2 < len(seg) && isIdentText(seg[start+1].text) && seg[start+2].text == "=" {
			ti := &TypeInfo{Name: seg[start+1].text, Kind: "type", StartLine: startLine, EndLine: endLine,
				Exported: headerHas(s, 0, len(seg), "pub")}
			ti.Fields = []Field{{Name: "underlying", Type: tokensText(seg[start+3:])}}
			return ti, true
		}
	}
	return nil, false
}

func jsTypeAlias(s *genSource, seg []genToken, startLine, endLine int) (*TypeInfo, bool) {
	start := 0
	for start < len(seg) && (seg[start].text == "export" || seg[start].text == "declare" || seg[start].text == "default") {
		start++
	}
	if start >= len(seg) || seg[start].text != "type" {
		return nil, false
	}
	if start+2 >= len(seg) || !isIdentText(seg[start+1].text) || seg[start+2].text != "=" {
		return nil, false
	}
	ti := &TypeInfo{Name: seg[start+1].text, Kind: "type", StartLine: startLine, EndLine: endLine,
		Exported: headerHas(s, 0, len(seg), "export")}
	ti.Fields = []Field{{Name: "underlying", Type: tokensText(seg[start+3:])}}
	return ti, true
}

func cAlias(s *genSource, seg []genToken, startLine, endLine int) (*TypeInfo, bool) {
	if len(seg) >= 2 && seg[0].text == "typedef" {
		name := ""
		for i := len(seg) - 1; i >= 1; i-- {
			if isIdentText(seg[i].text) {
				name = seg[i].text
				break
			}
		}
		if name == "" {
			return nil, false
		}
		return &TypeInfo{Name: name, Kind: "type", StartLine: startLine, EndLine: endLine, Exported: true}, true
	}
	if len(seg) >= 3 && seg[0].text == "using" && isIdentText(seg[1].text) && seg[2].text == "=" {
		return &TypeInfo{Name: seg[1].text, Kind: "type", StartLine: startLine, EndLine: endLine, Exported: true,
			Fields: []Field{{Name: "underlying", Type: tokensText(seg[3:])}}}, true
	}
	return nil, false
}

func recordFields(ts []genToken) []Field {
	if len(ts) == 0 || ts[0].text != "(" {
		return nil
	}
	depth := 0
	end := len(ts)
	for i, t := range ts {
		switch t.text {
		case "(":
			depth++
		case ")":
			depth--
		}
		if depth == 0 {
			end = i
			break
		}
	}
	var out []Field
	for _, part := range splitTopLevel(ts[1:end], ",") {
		part = stripMemberModifiers(part, famJava)
		if len(part) < 2 {
			continue
		}
		nameIdx := -1
		for i := len(part) - 1; i >= 0; i-- {
			if isIdentText(part[i].text) {
				nameIdx = i
				break
			}
		}
		if nameIdx <= 0 {
			continue
		}
		out = append(out, Field{Name: part[nameIdx].text, Type: tokensText(part[:nameIdx])})
	}
	return out
}

func tupleFields(ts []genToken, named bool) []Field {
	var out []Field
	parts := splitTopLevel(ts, ",")
	for idx, part := range parts {
		part = stripMemberModifiers(part, famRust)
		if len(part) == 0 {
			continue
		}
		if named {
			colon := -1
			for i, t := range part {
				if t.text == ":" {
					colon = i
					break
				}
			}
			if colon > 0 {
				name := part[colon-1].text
				out = append(out, Field{Name: name, Type: tokensText(part[colon+1:])})
				continue
			}
		}
		out = append(out, Field{Name: itoa(idx), Type: tokensText(part)})
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// ---------------- imports ----------------

func braceImports(s *genSource, fam genFamily) []string {
	switch fam {
	case famJS:
		return jsImports(s)
	case famRust:
		return rustImports(s)
	case famC:
		return cImports(s)
	case famJava:
		return javaImports(s)
	}
	return nil
}

func jsImports(s *genSource) []string {
	var out []string
	for _, t := range s.toks {
		switch t.text {
		case "from", "import", "require":
		default:
			continue
		}
		j := t.pos + len(t.text)
		for j < len(s.src) && (s.src[j] == ' ' || s.src[j] == '\t' || s.src[j] == '\r' || s.src[j] == '\n') {
			j++
		}
		if t.text == "import" || t.text == "require" {
			if j < len(s.src) && s.src[j] == '(' {
				j++
				for j < len(s.src) && (s.src[j] == ' ' || s.src[j] == '\t') {
					j++
				}
			}
		}
		if j >= len(s.src) {
			continue
		}
		if s.src[j] == '\'' || s.src[j] == '"' || s.src[j] == '`' {
			if val, ok := readQuotedAt(s.src, j); ok && val != "" {
				out = append(out, val)
			}
		}
	}
	return out
}

func readQuotedAt(src []byte, i int) (string, bool) {
	quote := src[i]
	j := i + 1
	start := j
	var b strings.Builder
	for j < len(src) {
		c := src[j]
		if c == '\\' && j+1 < len(src) {
			j += 2
			continue
		}
		if c == quote {
			b.Write(src[start:j])
			return b.String(), true
		}
		if c == '\n' {
			return "", false
		}
		j++
	}
	return "", false
}

func rustImports(s *genSource) []string {
	var out []string
	for i, t := range s.toks {
		if t.text == "extern" && i+2 < len(s.toks) && s.toks[i+1].text == "crate" && isIdentText(s.toks[i+2].text) {
			out = append(out, s.toks[i+2].text)
			continue
		}
		if t.text != "use" {
			continue
		}
		d := s.depth[i]
		var seq []genToken
		for j := i + 1; j < len(s.toks); j++ {
			if s.toks[j].text == ";" && s.depth[j] == d {
				break
			}
			seq = append(seq, s.toks[j])
		}
		out = append(out, expandUse(seq, "")...)
	}
	return out
}

func expandUse(ts []genToken, prefix string) []string {
	parts := splitTopLevel(ts, ",")
	var out []string
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		brace := -1
		for i, t := range part {
			if t.text == "{" {
				brace = i
				break
			}
		}
		if brace >= 0 {
			local := strings.TrimSuffix(useSegments(part[:brace]), "::")
			full := joinUsePath(prefix, local)
			depth := 1
			end := len(part)
			for i := brace + 1; i < len(part); i++ {
				switch part[i].text {
				case "{":
					depth++
				case "}":
					depth--
				}
				if depth == 0 {
					end = i
					break
				}
			}
			out = append(out, expandUse(part[brace+1:end], full)...)
			continue
		}
		// Drop `as` aliases.
		for i, t := range part {
			if t.text == "as" {
				part = part[:i]
				break
			}
		}
		local := useSegments(part)
		if local == "" {
			continue
		}
		if local == "self" {
			if prefix != "" {
				out = append(out, prefix)
			}
			continue
		}
		out = append(out, joinUsePath(prefix, local))
	}
	return out
}

func useSegments(ts []genToken) string {
	var b strings.Builder
	for _, t := range ts {
		if t.text == "pub" {
			continue
		}
		b.WriteString(t.text)
	}
	s := strings.TrimPrefix(strings.TrimSpace(b.String()), "::")
	return s
}

func joinUsePath(prefix, local string) string {
	if prefix == "" {
		return local
	}
	if local == "" {
		return prefix
	}
	return prefix + "::" + local
}

func cImports(s *genSource) []string {
	var out []string
	for ln := 1; ln <= s.lineCount(); ln++ {
		mt := strings.TrimSpace(s.maskLineString(ln))
		if !strings.HasPrefix(mt, "#") {
			continue
		}
		flat := strings.ReplaceAll(mt, " ", "")
		flat = strings.ReplaceAll(flat, "\t", "")
		if !strings.HasPrefix(flat, "#include") {
			continue
		}
		raw := s.lineString(ln)
		idx := strings.Index(raw, "include")
		if idx < 0 {
			continue
		}
		rest := raw[idx+len("include"):]
		if k := strings.Index(rest, "<"); k >= 0 {
			if e := strings.Index(rest[k:], ">"); e > 0 {
				out = append(out, rest[k+1:k+e])
			}
			continue
		}
		if k := strings.IndexAny(rest, "\"'"); k >= 0 {
			if val, ok := readQuotedAt([]byte(rest), k); ok {
				out = append(out, val)
			}
		}
	}
	return out
}

func javaImports(s *genSource) []string {
	var out []string
	for ln := 1; ln <= s.lineCount(); ln++ {
		mt := strings.TrimSpace(s.maskLineString(ln))
		if !strings.HasPrefix(mt, "import ") {
			continue
		}
		rest := strings.TrimSpace(mt[len("import "):])
		if strings.HasPrefix(rest, "static ") {
			rest = strings.TrimSpace(rest[len("static "):])
		}
		if idx := strings.IndexAny(rest, "; \t"); idx >= 0 {
			rest = rest[:idx]
		}
		if rest != "" {
			out = append(out, rest)
		}
	}
	return out
}

// ---------------- finalization ----------------

func finishGeneric(a *Analysis, s *genSource, funcs []*genericFunc, types []*TypeInfo, imports []string, engine *genericBigO) {
	sort.SliceStable(funcs, func(i, j int) bool {
		if funcs[i].fn.StartLine != funcs[j].fn.StartLine {
			return funcs[i].fn.StartLine < funcs[j].fn.StartLine
		}
		if funcs[i].fn.Name != funcs[j].fn.Name {
			return funcs[i].fn.Name < funcs[j].fn.Name
		}
		return funcs[i].fn.StartChar < funcs[j].fn.StartChar
	})
	local := make(map[string]bool, len(funcs))
	for _, f := range funcs {
		local[f.name] = true
	}
	var complexities []Complexity
	a.Functions = make([]Function, 0, len(funcs))
	for _, f := range funcs {
		res := engine.analyze(f)
		f.fn.BigO = res.C.String()
		f.fn.Evidence = res.Evidence
		f.fn.Recursive = res.Recursive
		f.calls = engine.callsOf(f)
		f.fn.Calls = filterCalls(f.calls, local)
		a.Functions = append(a.Functions, f.fn)
		complexities = append(complexities, res.C)
	}
	typeByName := make(map[string]*TypeInfo, len(types))
	for _, t := range types {
		if _, ok := typeByName[t.Name]; !ok {
			typeByName[t.Name] = t
		}
	}
	for _, f := range funcs {
		if f.recv == "" {
			continue
		}
		if ti := typeByName[f.recv]; ti != nil {
			addMethod(ti, Method{Name: f.name, Signature: f.fn.Signature})
		}
	}
	a.Types = make([]TypeInfo, 0, len(types))
	for _, t := range types {
		if t.Name == "" {
			continue
		}
		a.Types = append(a.Types, *t)
	}
	sort.SliceStable(a.Types, func(i, j int) bool {
		if a.Types[i].StartLine != a.Types[j].StartLine {
			return a.Types[i].StartLine < a.Types[j].StartLine
		}
		return a.Types[i].Name < a.Types[j].Name
	})
	a.Metrics.Types = len(a.Types)
	a.Imports = dedupeSorted(imports)
	if len(a.Imports) == 0 {
		a.Imports = nil
	}
	computeMetrics(a, complexities)
}

func dedupeSorted(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
