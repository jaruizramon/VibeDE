package analyze

import (
	"fmt"
	"sort"
	"strings"
)

// genericBigO computes static complexity estimates for token/indentation
// languages. Results are memoized per function and call-graph cycles degrade
// to O(1) so analysis always terminates.
type genRange struct {
	lo int
	hi int
}

type genericBigO struct {
	s        *genSource
	fam      genFamily
	free     map[string]*genericFunc
	methods  map[string]*genericFunc
	memo     map[*genericFunc]BigOResult
	visiting map[*genericFunc]bool

	nestedLines map[*genericFunc][]genRange
	nestedToks  map[*genericFunc][]genRange
}

func newGenericBigO(s *genSource, fam genFamily, funcs []*genericFunc) *genericBigO {
	e := &genericBigO{
		s:        s,
		fam:      fam,
		free:     make(map[string]*genericFunc),
		methods:  make(map[string]*genericFunc),
		memo:     make(map[*genericFunc]BigOResult),
		visiting: make(map[*genericFunc]bool),

		nestedLines: make(map[*genericFunc][]genRange, len(funcs)),
		nestedToks:  make(map[*genericFunc][]genRange, len(funcs)),
	}
	for _, f := range funcs {
		if f.name == "" {
			continue
		}
		if f.recv != "" {
			key := f.recv + "." + f.name
			if _, ok := e.methods[key]; !ok {
				e.methods[key] = f
			}
			continue
		}
		if _, ok := e.free[f.name]; !ok {
			e.free[f.name] = f
		}
	}
	// Precompute which functions are nested inside other functions so
	// per-token/per-line checks stay cheap.
	for _, f := range funcs {
		if f.bodyLo < 0 {
			continue
		}
		for _, g := range funcs {
			if g == f || g.bodyLo < 0 || g.python != f.python {
				continue
			}
			if f.python {
				if g.fn.StartLine > f.fn.StartLine && g.fn.EndLine <= f.fn.EndLine {
					e.nestedLines[f] = append(e.nestedLines[f],
						genRange{lo: g.fn.StartLine, hi: g.fn.EndLine})
				}
				continue
			}
			if g.bodyLo >= f.bodyLo && g.bodyHi <= f.bodyHi {
				lo := g.bodyLo - 1
				if lo < 0 {
					lo = 0
				}
				hi := g.bodyHi + 1
				if hi >= len(s.toks) {
					hi = len(s.toks) - 1
				}
				e.nestedToks[f] = append(e.nestedToks[f], genRange{lo: lo, hi: hi})
			}
		}
	}
	return e
}

func (e *genericBigO) analyze(f *genericFunc) BigOResult {
	if f == nil {
		return BigOResult{C: O1()}
	}
	if r, ok := e.memo[f]; ok {
		return r
	}
	if e.visiting[f] {
		return BigOResult{C: O1()}
	}
	e.visiting[f] = true
	defer delete(e.visiting, f)

	var res BigOResult
	if f.python {
		res = e.pythonBase(f)
	} else {
		res = e.braceBase(f)
	}
	rec := e.recursion(f)
	if rec.Recursive {
		res.C = res.C.Max(rec.C)
		res.Evidence = append(res.Evidence, rec.Evidence...)
		res.Recursive = true
	}
	if res.C.IsConstant() && len(res.Evidence) == 0 {
		res.Evidence = []string{"no loops or recursion detected → O(1)"}
	}
	if len(res.Evidence) > 30 {
		res.Evidence = res.Evidence[:30]
	}
	res.Calls = e.callsOf(f)
	e.memo[f] = res
	return res
}

func (e *genericBigO) callsOf(f *genericFunc) []string {
	if f.bodyLo < 0 || f.bodyHi < f.bodyLo {
		return nil
	}
	sites := callSitesInRange(e.s, f.bodyLo, f.bodyHi)
	out := make([]string, 0, len(sites))
	for _, cs := range sites {
		if f.python {
			if e.pythonNestedLine(f, cs.line) {
				continue
			}
		} else if e.insideNestedBrace(f, cs.tok) {
			continue
		}
		out = append(out, cs.name)
	}
	return out
}

func (e *genericBigO) resolveCall(name, recv string, from *genericFunc) *genericFunc {
	if name == "" {
		return nil
	}
	if recv == "" {
		if f := e.free[name]; f != nil {
			return f
		}
		if from != nil && from.recv != "" {
			if f := e.methods[from.recv+"."+name]; f != nil {
				return f
			}
		}
		return nil
	}
	switch recv {
	case "self", "this":
		if from != nil && from.recv != "" {
			if f := e.methods[from.recv+"."+name]; f != nil {
				return f
			}
		}
		return nil
	case "super":
		return e.uniqueMethod(name, from)
	}
	if f := e.methods[recv+"."+name]; f != nil {
		return f
	}
	if from != nil && recv == from.recv {
		if f := e.methods[recv+"."+name]; f != nil {
			return f
		}
	}
	// Qualified free function (e.g. C++ `ns::helper`).
	if f := e.free[name]; f != nil && (e.fam == famC || e.fam == famRust) {
		return f
	}
	return nil
}

func (e *genericBigO) uniqueMethod(name string, from *genericFunc) *genericFunc {
	var found *genericFunc
	for _, f := range e.methods {
		if f.name != name || f == from {
			continue
		}
		if found != nil {
			return nil
		}
		found = f
	}
	return found
}

func (e *genericBigO) isSelfCall(name, recv string, f *genericFunc) bool {
	if f == nil || name != f.name {
		return false
	}
	switch recv {
	case "":
		if f.recv != "" {
			if free := e.free[name]; free != nil && free != f {
				return false
			}
		}
		return true
	case "self", "this":
		return true
	}
	return recv == f.recv
}

// ---------------- call sites and library costs ----------------

type genCallSite struct {
	name string
	recv string
	tok  int
	line int
}

func callSitesInRange(s *genSource, lo, hi int) []genCallSite {
	if lo < 0 {
		lo = 0
	}
	if hi >= len(s.toks) {
		hi = len(s.toks) - 1
	}
	var out []genCallSite
	for i := lo; i+1 <= hi && i+1 < len(s.toks); i++ {
		t := s.toks[i]
		if !isIdentText(t.text) {
			continue
		}
		if s.toks[i+1].text != "(" {
			continue
		}
		if i > 0 {
			switch s.toks[i-1].text {
			case "function", "fn", "def", "new", "class", "struct",
				"interface", "enum", "trait", "type":
				continue
			}
		}
		if genNonCallNames[t.text] || genBlockKeywords[t.text] {
			continue
		}
		recv := ""
		if i >= 2 {
			switch s.toks[i-1].text {
			case ".", "::", "->":
				if isIdentText(s.toks[i-2].text) {
					recv = s.toks[i-2].text
				}
			}
		}
		out = append(out, genCallSite{name: t.text, recv: recv, tok: i, line: t.line})
	}
	return out
}

func libraryCallCost(fam genFamily, name, recv string) (Complexity, string) {
	n := strings.ToLower(name)
	lrecv := strings.ToLower(recv)
	switch fam {
	case famPython:
		switch n {
		case "sorted":
			return ONLogN(), "sort call"
		case "sort":
			if recv != "" {
				return ONLogN(), "sort call"
			}
		case "bisect", "bisect_left", "bisect_right", "insort":
			return OLogN(), "binary search"
		case "sum", "list", "set", "dict", "tuple", "join", "split", "rsplit",
			"replace", "extend", "copy", "strip", "lstrip", "rstrip", "lower",
			"upper", "title", "find", "rfind", "index", "count", "partition":
			return ON(), "linear scan"
		}
	case famJS:
		switch n {
		case "sort", "tosorted":
			return ONLogN(), "sort call"
		case "includes", "indexof", "lastindexof", "find", "findindex",
			"filter", "map", "foreach", "reduce", "reduceright", "some",
			"every", "flat", "flatmap", "join", "concat", "slice", "splice",
			"split", "replace", "replaceall", "match", "matchall", "search",
			"trim", "padstart", "padend":
			return ON(), "linear scan"
		case "keys", "values", "entries":
			if lrecv == "object" {
				return ON(), "linear scan"
			}
		case "parse", "stringify":
			if lrecv == "json" {
				return ON(), "linear scan"
			}
		case "from":
			if lrecv == "array" {
				return ON(), "linear scan"
			}
		}
	case famRust:
		switch n {
		case "sort", "sort_unstable", "sort_by", "sort_by_key",
			"sort_unstable_by", "sort_unstable_by_key", "sort_by_cached_key":
			return ONLogN(), "sort call"
		case "binary_search", "binary_search_by", "binary_search_by_key",
			"partition_point":
			return OLogN(), "binary search"
		case "collect", "clone", "contains", "starts_with", "ends_with",
			"find", "position", "extend", "extend_from_slice", "to_vec",
			"to_owned", "retain", "drain":
			return ON(), "linear scan"
		}
	case famC, famJava:
		switch n {
		case "sort", "stable_sort", "qsort", "partial_sort", "sort_by":
			return ONLogN(), "sort call"
		case "lower_bound", "upper_bound", "binary_search", "equal_range",
			"bsearch", "binarysearch":
			return OLogN(), "binary search"
		case "strlen", "strcpy", "strncpy", "strcat", "strcmp", "strncmp",
			"memcpy", "memmove", "memset", "find", "count", "contains",
			"indexof", "lastindexof", "split", "replace", "replaceall",
			"substring", "join", "tostring", "equals", "foreach", "stream",
			"collect", "filter", "map", "reduce":
			return ON(), "linear scan"
		}
	}
	return O1(), ""
}

// ---------------- Python ----------------

type pyLoop struct {
	line    int
	indent  int
	endLine int
	factor  Complexity
	cum     Complexity
	depth   int
}

func (e *genericBigO) pythonBase(f *genericFunc) BigOResult {
	res := BigOResult{C: O1()}
	if f.bodyLo < 0 || f.bodyHi < f.bodyLo || f.lineHi < f.lineLo {
		return res
	}
	loops := e.pyLoops(f)
	nested := func(line int) bool { return e.pythonNestedLine(f, line) }
	for _, lp := range loops {
		res.C = res.C.Max(lp.cum)
		if lp.cum.IsConstant() {
			res.Evidence = append(res.Evidence,
				fmt.Sprintf("constant-bounded loop at line %d → O(1)", lp.line))
		} else if lp.depth > 0 {
			res.Evidence = append(res.Evidence,
				fmt.Sprintf("nested loop at line %d → %s", lp.line, lp.cum))
		} else if lp.factor.NPow == 0 && lp.factor.LogPow > 0 {
			res.Evidence = append(res.Evidence,
				fmt.Sprintf("halving loop at line %d → %s", lp.line, lp.cum))
		} else {
			res.Evidence = append(res.Evidence,
				fmt.Sprintf("loop at line %d → %s", lp.line, lp.cum))
		}
	}
	comp, compEvidence := e.pyComprehensions(f, loops, nested)
	res.Evidence = append(res.Evidence, compEvidence...)
	for ln, c := range comp {
		res.C = res.C.Max(c.Mul(pyLineProduct(loops, ln)))
	}
	sites := callSitesInRange(e.s, f.bodyLo, f.bodyHi)
	for _, cs := range sites {
		if nested(cs.line) {
			continue
		}
		if e.isSelfCall(cs.name, cs.recv, f) {
			continue
		}
		var cost Complexity
		var label string
		if callee := e.resolveCall(cs.name, cs.recv, f); callee != nil && callee != f {
			r := e.analyze(callee)
			cost = r.C
			if !cost.IsConstant() {
				res.Evidence = append(res.Evidence,
					fmt.Sprintf("calls %s() at line %d → %s", cs.name, cs.line, cost))
			}
		} else {
			cost, label = libraryCallCost(famPython, cs.name, cs.recv)
			if label != "" {
				res.Evidence = append(res.Evidence,
					fmt.Sprintf("%s at line %d → %s", label, cs.line, cost))
			}
		}
		prod := pyLineProduct(loops, cs.line)
		if c, ok := comp[cs.line]; ok {
			prod = prod.Mul(c)
		}
		res.C = res.C.Max(cost.Mul(prod))
	}
	return res
}

func (e *genericBigO) pyLoops(f *genericFunc) []pyLoop {
	var loops []pyLoop
	lineIndent := func(line int) int { return e.s.lineIndent(line) }
	for ln := f.lineLo; ln <= f.lineHi; ln++ {
		if e.pythonNestedLine(f, ln) {
			continue
		}
		mt := strings.TrimSpace(e.s.maskLineString(ln))
		var factor Complexity
		var isLoop bool
		switch {
		case strings.HasPrefix(mt, "for ") || strings.HasPrefix(mt, "async for "):
			isLoop = true
			factor = pyForFactor(mt)
		case strings.HasPrefix(mt, "while "):
			isLoop = true
			factor = ON()
		}
		if !isLoop {
			continue
		}
		indent := lineIndent(ln)
		end := ln
		for next := ln + 1; next <= f.lineHi; next++ {
			if strings.TrimSpace(e.s.maskLineString(next)) == "" {
				continue
			}
			if lineIndent(next) <= indent {
				break
			}
			end = next
		}
		lp := pyLoop{line: ln, indent: indent, endLine: end, factor: factor}
		if strings.HasPrefix(mt, "while ") {
			condLo, condHi := e.lineTokenRange(ln)
			condStart := -1
			for i := condLo; i <= condHi && i >= 0; i++ {
				if e.s.toks[i].text == "while" {
					condStart = i + 1
					break
				}
			}
			bodyLo, bodyHi := -1, -1
			if end > ln {
				bodyLo, bodyHi = tokenRangeForLines(e.s, ln+1, end)
			} else if condStart >= 0 {
				for i := condStart; i <= condHi; i++ {
					if e.s.toks[i].text == ":" {
						bodyLo, bodyHi = i+1, condHi
						break
					}
				}
			}
			if bodyLo >= 0 && condStart >= 0 {
				condEnd := condHi
				for i := condStart; i <= condHi; i++ {
					if e.s.toks[i].text == ":" {
						condEnd = i - 1
						break
					}
				}
				if loopHasLog(e.s, bodyLo, bodyHi, condStart, condEnd) {
					lp.factor = OLogN()
				}
			}
		}
		lp.cum = lp.factor
		loops = append(loops, lp)
	}
	for i := range loops {
		depth := 0
		cum := loops[i].factor
		for j := range loops {
			if i == j {
				continue
			}
			if loops[j].line < loops[i].line && loops[j].endLine >= loops[i].line &&
				loops[j].indent < loops[i].indent {
				depth++
				cum = cum.Mul(loops[j].factor)
			}
		}
		loops[i].depth = depth
		loops[i].cum = cum
	}
	sort.SliceStable(loops, func(i, j int) bool { return loops[i].line < loops[j].line })
	return loops
}

func pyForFactor(mt string) Complexity {
	idx := strings.Index(mt, " in ")
	if idx < 0 {
		return ON()
	}
	iter := strings.TrimSpace(mt[idx+len(" in "):])
	if c := strings.LastIndex(iter, ":"); c >= 0 {
		iter = strings.TrimSpace(iter[:c])
	}
	if strings.HasPrefix(iter, "range(") && strings.HasSuffix(iter, ")") {
		inner := iter[len("range(") : len(iter)-1]
		args := strings.Split(inner, ",")
		constant := true
		any := false
		for _, a := range args {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			any = true
			if !isIntText(a) {
				constant = false
			}
		}
		if any && constant {
			return O1()
		}
	}
	return ON()
}

func pyLineProduct(loops []pyLoop, line int) Complexity {
	c := O1()
	for _, lp := range loops {
		if line > lp.line && line <= lp.endLine {
			c = c.Mul(lp.factor)
		}
	}
	return c
}

func (e *genericBigO) pyComprehensions(f *genericFunc, loops []pyLoop, nested func(int) bool) (map[int]Complexity, []string) {
	out := make(map[int]Complexity)
	var evidence []string
	for ln := f.lineLo; ln <= f.lineHi; ln++ {
		if nested(ln) {
			continue
		}
		lo, hi := e.lineTokenRange(ln)
		if lo < 0 {
			continue
		}
		first := e.s.toks[lo].text
		k := 0
		for i := lo; i <= hi; i++ {
			if e.s.toks[i].text != "for" {
				continue
			}
			if i == lo || (first == "async" && i == lo+1) {
				continue
			}
			k++
		}
		if k == 0 {
			continue
		}
		c := O1()
		for j := 0; j < k; j++ {
			c = c.Mul(ON())
		}
		out[ln] = c
		cum := c.Mul(pyLineProduct(loops, ln))
		evidence = append(evidence, fmt.Sprintf("comprehension at line %d → %s", ln, cum))
	}
	return out, evidence
}

func (e *genericBigO) pythonNestedLine(f *genericFunc, line int) bool {
	for _, r := range e.nestedLines[f] {
		if line >= r.lo && line <= r.hi {
			return true
		}
	}
	return false
}

func (e *genericBigO) lineTokenRange(ln int) (int, int) {
	if len(e.s.toks) == 0 {
		return -1, -1
	}
	first := sort.Search(len(e.s.toks), func(i int) bool { return e.s.toks[i].line >= ln })
	last := sort.Search(len(e.s.toks), func(i int) bool { return e.s.toks[i].line > ln })
	if first >= last {
		return -1, -1
	}
	return first, last - 1
}

// ---------------- brace languages ----------------

type braceLoop struct {
	line   int
	factor Complexity
	cum    Complexity
	depth  int
	bodyLo int
	bodyHi int
	kind   string
}

func (e *genericBigO) braceBase(f *genericFunc) BigOResult {
	res := BigOResult{C: O1()}
	if f.bodyLo < 0 || f.bodyHi < f.bodyLo {
		return res
	}
	loops := e.braceLoops(f)
	for _, lp := range loops {
		res.C = res.C.Max(lp.cum)
		if lp.kind == "callback" {
			if lp.depth > 0 {
				res.Evidence = append(res.Evidence,
					fmt.Sprintf("nested iteration callback at line %d → %s", lp.line, lp.cum))
			} else {
				res.Evidence = append(res.Evidence,
					fmt.Sprintf("iteration callback at line %d → %s", lp.line, lp.cum))
			}
			continue
		}
		switch {
		case lp.cum.IsConstant():
			res.Evidence = append(res.Evidence,
				fmt.Sprintf("constant-bounded loop at line %d → O(1)", lp.line))
		case lp.depth > 0:
			res.Evidence = append(res.Evidence,
				fmt.Sprintf("nested loop at line %d → %s", lp.line, lp.cum))
		case lp.factor.NPow == 0 && lp.factor.LogPow > 0:
			res.Evidence = append(res.Evidence,
				fmt.Sprintf("halving loop at line %d → %s", lp.line, lp.cum))
		default:
			res.Evidence = append(res.Evidence,
				fmt.Sprintf("loop at line %d → %s", lp.line, lp.cum))
		}
	}
	sites := callSitesInRange(e.s, f.bodyLo, f.bodyHi)
	for _, cs := range sites {
		if e.insideNestedBrace(f, cs.tok) {
			continue
		}
		if e.isSelfCall(cs.name, cs.recv, f) {
			continue
		}
		var cost Complexity
		var label string
		if callee := e.resolveCall(cs.name, cs.recv, f); callee != nil && callee != f {
			r := e.analyze(callee)
			cost = r.C
			if !cost.IsConstant() {
				res.Evidence = append(res.Evidence,
					fmt.Sprintf("calls %s() at line %d → %s", cs.name, cs.line, cost))
			}
		} else {
			cost, label = libraryCallCost(e.fam, cs.name, cs.recv)
			if label != "" {
				res.Evidence = append(res.Evidence,
					fmt.Sprintf("%s at line %d → %s", label, cs.line, cost))
			}
		}
		prod := braceContainerProduct(loops, cs.tok)
		res.C = res.C.Max(cost.Mul(prod))
	}
	return res
}

func (e *genericBigO) braceLoops(f *genericFunc) []braceLoop {
	var loops []braceLoop
	skipWhile := make(map[int]bool)
	iteration := map[string]bool{
		"foreach": true, "for_each": true, "map": true, "filter": true, "reduce": true,
		"reduceright": true, "some": true, "every": true, "find": true,
		"findindex": true, "flatmap": true,
	}
	for i := f.bodyLo; i <= f.bodyHi && i < len(e.s.toks); i++ {
		if e.insideNestedBrace(f, i) {
			continue
		}
		t := e.s.toks[i].text
		if t == "do" {
			bodyLo, bodyHi := e.s.blockBody(i+1, f.bodyHi)
			lp := braceLoop{line: e.s.toks[i].line, factor: ON(), bodyLo: bodyLo, bodyHi: bodyHi}
			// The trailing `while (...) ;` carries the loop condition.
			whileIdx := bodyHi + 1
			if whileIdx < len(e.s.toks) && e.s.toks[whileIdx].text == "}" {
				whileIdx++
			}
			if whileIdx < len(e.s.toks) && e.s.toks[whileIdx].text == "while" {
				if _, cLo, cHi, ok := e.loopParts(whileIdx, f.bodyHi); ok &&
					loopHasLog(e.s, bodyLo, bodyHi, cLo, cHi) {
					lp.factor = OLogN()
				}
				skipWhile[whileIdx] = true
			}
			loops = append(loops, lp)
			continue
		}
		if t == "loop" && e.fam == famRust {
			bodyLo, bodyHi := e.s.blockBody(i+1, f.bodyHi)
			loops = append(loops, braceLoop{
				line: e.s.toks[i].line, factor: ON(), bodyLo: bodyLo, bodyHi: bodyHi,
			})
			continue
		}
		if t == "for" || t == "while" {
			if t == "while" && skipWhile[i] {
				continue
			}
			hdrLo, hdrHi, bodyStart, ok := e.loopParts(i, f.bodyHi)
			if !ok {
				continue
			}
			bodyLo, bodyHi := e.s.blockBody(bodyStart, f.bodyHi)
			lp := braceLoop{line: e.s.toks[i].line, bodyLo: bodyLo, bodyHi: bodyHi}
			if t == "for" {
				if forConstantBound(e.s.toks[hdrLo : hdrHi+1]) {
					lp.factor = O1()
				} else if loopHasLog(e.s, bodyLo, bodyHi, hdrLo, hdrHi) {
					lp.factor = OLogN()
				} else {
					lp.factor = ON()
				}
			} else {
				if loopHasLog(e.s, bodyLo, bodyHi, hdrLo, hdrHi) {
					lp.factor = OLogN()
				} else {
					lp.factor = ON()
				}
			}
			loops = append(loops, lp)
			continue
		}
		// Iteration callbacks: .map(x => ...), .forEach(function () {...}).
		if isIdentText(t) && i+1 <= f.bodyHi && e.s.toks[i+1].text == "(" &&
			i >= 1 && e.s.toks[i-1].text == "." {
			name := strings.ToLower(t)
			if iteration[name] {
				close := e.s.match[i+1]
				if close < 0 || close > f.bodyHi {
					close = f.bodyHi
				}
				loops = append(loops, braceLoop{
					line: e.s.toks[i].line, factor: ON(), kind: "callback",
					bodyLo: i + 2, bodyHi: close - 1,
				})
			}
		}
	}
	for i := range loops {
		depth := 0
		cum := loops[i].factor
		for j := range loops {
			if i == j {
				continue
			}
			if loops[j].bodyLo <= loops[i].bodyLo && loops[i].bodyHi <= loops[j].bodyHi &&
				(loops[j].bodyHi-loops[j].bodyLo) > (loops[i].bodyHi-loops[i].bodyLo) {
				depth++
				cum = cum.Mul(loops[j].factor)
			}
		}
		loops[i].depth = depth
		loops[i].cum = cum
	}
	sort.SliceStable(loops, func(i, j int) bool {
		if loops[i].line != loops[j].line {
			return loops[i].line < loops[j].line
		}
		return loops[i].bodyLo < loops[j].bodyLo
	})
	return loops
}

func (e *genericBigO) loopParts(kw, limit int) (int, int, int, bool) {
	if kw+1 > limit {
		return 0, 0, 0, false
	}
	if e.s.toks[kw+1].text == "(" {
		close := e.s.match[kw+1]
		if close < 0 {
			return kw + 1, limit, limit + 1, false
		}
		return kw + 2, close - 1, close + 1, true
	}
	for j := kw + 1; j <= limit; j++ {
		if e.s.toks[j].text == "{" {
			return kw + 1, j - 1, j, true
		}
		if e.s.toks[j].text == ";" {
			return kw + 1, j - 1, j + 1, true
		}
	}
	return kw + 1, limit, limit + 1, false
}

func (s *genSource) blockBody(start, limit int) (int, int) {
	if start > limit || start < 0 {
		return start, start - 1
	}
	if start >= len(s.toks) {
		return start, limit
	}
	if s.toks[start].text == "{" {
		m := s.match[start]
		if m < 0 || m > limit {
			m = limit + 1
		}
		return start + 1, m - 1
	}
	d := s.depth[start]
	for k := start; k <= limit && k < len(s.toks); k++ {
		if s.toks[k].text == "{" {
			m := s.match[k]
			if m > 0 && m <= limit {
				return start, m
			}
			return start, limit
		}
		if s.toks[k].text == ";" && s.depth[k] == d {
			return start, k
		}
	}
	return start, limit
}

func braceContainerProduct(loops []braceLoop, tok int) Complexity {
	c := O1()
	for _, lp := range loops {
		if lp.bodyLo <= tok && tok <= lp.bodyHi {
			c = c.Mul(lp.factor)
		}
	}
	return c
}

func (e *genericBigO) insideNestedBrace(f *genericFunc, tok int) bool {
	for _, r := range e.nestedToks[f] {
		if tok >= r.lo && tok <= r.hi {
			return true
		}
	}
	return false
}

func forConstantBound(hdr []genToken) bool {
	if len(hdr) == 0 {
		return false
	}
	// C-style condition.
	semis := make([]int, 0, 2)
	for i, t := range hdr {
		if t.text == ";" {
			semis = append(semis, i)
		}
	}
	if len(semis) >= 2 {
		cond := hdr[semis[0]+1 : semis[1]]
		cmp := -1
		for i, t := range cond {
			switch t.text {
			case "<", "<=", ">", ">=", "!=", "==":
				cmp = i
			}
		}
		if cmp >= 0 {
			left := cond[:cmp]
			right := cond[cmp+1:]
			if simpleInt(left) || simpleInt(right) {
				return true
			}
		}
	}
	// Rust range literal.
	for i, t := range hdr {
		if t.text == ".." || t.text == "..=" {
			rest := hdr[i+1:]
			if simpleInt(rest) {
				return true
			}
			break
		}
	}
	// JS for-of over a small literal array.
	of := -1
	for i, t := range hdr {
		if t.text == "of" || t.text == "in" {
			of = i
		}
	}
	if of >= 0 {
		rest := hdr[of+1:]
		if len(rest) >= 3 && rest[0].text == "[" && rest[len(rest)-1].text == "]" {
			items := rest[1 : len(rest)-1]
			if len(items) <= 16 {
				simple := true
				for _, it := range items {
					if it.text != "," && !isIntText(it.text) {
						simple = false
						break
					}
				}
				if simple {
					return true
				}
			}
		}
	}
	return false
}

func simpleInt(ts []genToken) bool {
	return len(ts) == 1 && isIntText(ts[0].text)
}

// loopHasLog reports whether a loop whose condition identifiers are
// condLo..condHi performs logarithmic halving/doubling inside its body.
func loopHasLog(s *genSource, bodyLo, bodyHi, condLo, condHi int) bool {
	if bodyLo < 0 || bodyHi < bodyLo || condLo < 0 || condHi < condLo {
		return false
	}
	if condHi >= len(s.toks) {
		condHi = len(s.toks) - 1
	}
	if bodyHi >= len(s.toks) {
		bodyHi = len(s.toks) - 1
	}
	if bodyLo >= len(s.toks) {
		return false
	}
	vars := assignedHalvingVars(s.toks[bodyLo : bodyHi+1])
	if len(vars) == 0 {
		return false
	}
	condVars := identVars(s.toks[condLo : condHi+1])
	for v := range vars {
		if condVars[v] {
			return true
		}
	}
	return assignsCondFromVars(s.toks[bodyLo:bodyHi+1], condVars, vars)
}

func identVars(ts []genToken) map[string]bool {
	out := make(map[string]bool)
	for _, t := range ts {
		if isIdentText(t.text) {
			out[t.text] = true
		}
	}
	return out
}

// assignedHalvingVars finds variables assigned a division/shift/multiplication
// or a slice around a midpoint variable in a token stream.
func assignedHalvingVars(ts []genToken) map[string]bool {
	out := make(map[string]bool)
	for i := 0; i < len(ts); i++ {
		if !isIdentText(ts[i].text) {
			continue
		}
		name := ts[i].text
		if i+1 >= len(ts) {
			continue
		}
		op := ts[i+1].text
		j := i + 2
		end := j
		for end < len(ts) && end-j < 24 {
			t := ts[end].text
			if t == ";" || t == "{" || t == "}" {
				break
			}
			if end > j && ts[end].line != ts[i].line {
				break
			}
			end++
		}
		rhs := ts[j:end]
		switch op {
		case "/=", "//=", ">>=", ">>>=":
			out[name] = true
			continue
		case "*=", "<<=":
			if len(rhs) > 0 && isIntText(rhs[0].text) && rhs[0].text != "0" && rhs[0].text != "1" {
				out[name] = true
			}
			continue
		case "=":
			for k := 0; k+1 < len(rhs); k++ {
				switch rhs[k].text {
				case "/", "//", "*":
					if isIntText(rhs[k+1].text) && rhs[k+1].text != "0" && rhs[k+1].text != "1" {
						out[name] = true
					}
				case ">>", ">>>", "<<":
					if isIntText(rhs[k+1].text) && rhs[k+1].text != "0" {
						out[name] = true
					}
				}
			}
		}
	}
	// Two-pass: slice bounds around a known midpoint variable are halving too.
	changed := true
	for changed {
		changed = false
		for i := 0; i < len(ts); i++ {
			if !isIdentText(ts[i].text) || i+1 >= len(ts) || ts[i+1].text != "=" {
				continue
			}
			name := ts[i].text
			if out[name] {
				continue
			}
			for j := i + 2; j < len(ts) && j-i < 24; j++ {
				if ts[j].text == ";" || ts[j].text == "{" || ts[j].text == "}" {
					break
				}
				if ts[j].text == "[" {
					for k := j + 1; k < len(ts) && k-j < 12; k++ {
						if ts[k].text == "]" || ts[k].text == ";" {
							break
						}
						if isIdentText(ts[k].text) && out[ts[k].text] {
							out[name] = true
							changed = true
							break
						}
					}
					break
				}
			}
		}
	}
	return out
}

func assignsCondFromVars(ts []genToken, cond, vars map[string]bool) bool {
	for i := 0; i+1 < len(ts); i++ {
		if !isIdentText(ts[i].text) || !cond[ts[i].text] || ts[i+1].text != "=" {
			continue
		}
		for j := i + 2; j < len(ts) && j-i < 12; j++ {
			if ts[j].text == ";" || ts[j].text == "{" || ts[j].text == "}" {
				break
			}
			if isIdentText(ts[j].text) && vars[ts[j].text] {
				return true
			}
		}
	}
	return false
}

// ---------------- recursion ----------------

func (e *genericBigO) recursion(f *genericFunc) BigOResult {
	if f.bodyLo < 0 || f.bodyHi < f.bodyLo {
		return BigOResult{}
	}
	sites := callSitesInRange(e.s, f.bodyLo, f.bodyHi)
	var rec []genCallSite
	for _, cs := range sites {
		if !e.isSelfCall(cs.name, cs.recv, f) {
			continue
		}
		if f.python {
			if e.pythonNestedLine(f, cs.line) {
				continue
			}
		} else if e.insideNestedBrace(f, cs.tok) {
			continue
		}
		rec = append(rec, cs)
	}
	if len(rec) == 0 {
		return BigOResult{}
	}
	vars := assignedHalvingVars(e.s.toks[f.bodyLo : f.bodyHi+1])
	halving, decrement, linear := false, false, false
	var loopProduct func(tok, line int) Complexity
	if f.python {
		loops := e.pyLoops(f)
		loopProduct = func(_ int, line int) Complexity { return pyLineProduct(loops, line) }
	} else {
		loops := e.braceLoops(f)
		loopProduct = func(tok int, _ int) Complexity { return braceContainerProduct(loops, tok) }
	}
	maxProduct := O1()
	for _, cs := range rec {
		args := callArgs(e.s, cs.tok)
		for _, arg := range args {
			h, d, l := classifyRecursiveArg(arg, vars)
			halving = halving || h
			decrement = decrement || d
			linear = linear || l
		}
		maxProduct = maxProduct.Max(loopProduct(cs.tok, cs.line))
	}
	var c Complexity
	var shape string
	switch {
	case len(rec) == 1 && halving:
		c, shape = OLogN(), "single halving recursive call → O(log n)"
	case len(rec) >= 2 && halving && !decrement:
		c, shape = ONLogN(), "two divide-and-conquer recursive calls → O(n log n)"
	case len(rec) >= 2 && decrement:
		c, shape = OExp(), "overlapping recursive calls with decremented input → O(2^n)"
	case len(rec) == 1:
		c, shape = ON(), "single recursive call → O(n)"
	case !halving && !decrement && linear:
		c, shape = ON(), "recursive traversal → O(n)"
	default:
		c, shape = ON(), "multiple recursive calls → O(n)"
	}
	evidence := []string{fmt.Sprintf("recursive call at line %d; %s", rec[0].line, shape)}
	if !maxProduct.IsConstant() {
		c = c.Mul(maxProduct)
		evidence = append(evidence,
			fmt.Sprintf("recursive call inside a loop at line %d → %s", rec[0].line, c))
	}
	return BigOResult{
		Recursive: true,
		C:         c,
		Evidence:  evidence,
	}
}

func callArgs(s *genSource, tok int) [][]genToken {
	if tok+1 >= len(s.toks) || s.toks[tok+1].text != "(" {
		return nil
	}
	open := tok + 1
	close := s.match[open]
	if close < 0 {
		close = len(s.toks)
	}
	if close <= open+1 {
		return nil
	}
	return splitTopLevel(s.toks[open+1:close], ",")
}

func classifyRecursiveArg(ts []genToken, halvingVars map[string]bool) (halving, decrement, linear bool) {
	if len(ts) == 0 {
		return false, false, false
	}
	if len(ts) == 1 && isIntText(ts[0].text) {
		return false, false, false
	}
	hasIdent := false
	for i := 0; i < len(ts); i++ {
		t := ts[i]
		switch t.text {
		case ">>", ">>>", "<<":
			if i+1 < len(ts) && isIntText(ts[i+1].text) {
				halving = true
			}
		case "/", "//":
			if i+1 < len(ts) && isIntText(ts[i+1].text) && ts[i+1].text != "1" {
				halving = true
			}
		case "-":
			if i+1 < len(ts) && isIntText(ts[i+1].text) {
				decrement = true
			}
		case "*":
			if i+1 < len(ts) && isIntText(ts[i+1].text) {
				linear = true
			}
		}
		if isIdentText(t.text) {
			hasIdent = true
			if halvingVars[t.text] {
				halving = true
			}
			switch t.text {
			case "len", "size", "length", "count":
				// f(len(xs)/2) is handled by the division above.
			}
		}
	}
	if halving || decrement {
		return halving, decrement, hasIdent
	}
	return false, false, hasIdent
}
