package analyze

import (
	"bytes"
	"path/filepath"
	"sort"
	"strings"
)

// analyzeCSS extracts selectors and declarations from CSS, SCSS or LESS.
// Comments and strings are masked before structural scanning, so braces and
// semicolons inside them never affect the outline.
func analyzeCSS(path string, src []byte) (Analysis, error) {
	lang := "css"
	style := lexCSS
	switch strings.ToLower(filepath.Ext(path)) {
	case ".scss", ".sass":
		lang = "scss"
		style = lexPreproc
	case ".less":
		lang = "less"
		style = lexPreproc
	}
	a := Analysis{
		Path:      path,
		Language:  lang,
		Bytes:     int64(len(src)),
		Lines:     countLines(src),
		SLOC:      countSLOC(src),
		Functions: []Function{},
		Types:     []TypeInfo{},
	}
	a.Rules = parseCSSRules(src, style)
	computeMetrics(&a, nil)
	a.Notes = append(a.Notes,
		"CSS analysis is a tolerant rule scanner; nested preprocessor selectors are reported as written.")
	return a, nil
}

var cssContainerAtRules = map[string]bool{
	"media": true, "supports": true, "container": true, "layer": true,
	"scope": true, "document": true, "starting-style": true,
}

func parseCSSRules(src []byte, style lexStyle) []CSSRule {
	mask := maskSource(src, style)
	starts := srcLineStarts(src)
	var rules []CSSRule
	parseCSSBlock(src, mask, starts, 0, len(mask), 0, &rules)
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Line != rules[j].Line {
			return rules[i].Line < rules[j].Line
		}
		return rules[i].Selector < rules[j].Selector
	})
	return rules
}

func parseCSSBlock(src, mask []byte, starts []int, lo, hi, depth int, rules *[]CSSRule) {
	if depth > 100 || lo >= hi {
		return
	}
	segStart := lo
	i := lo
	paren, bracket := 0, 0
	for i < hi {
		switch mask[i] {
		case '(':
			paren++
		case ')':
			if paren > 0 {
				paren--
			}
		case '[':
			bracket++
		case ']':
			if bracket > 0 {
				bracket--
			}
		case ';':
			if paren == 0 && bracket == 0 {
				segStart = i + 1
			}
		case '}':
			if paren == 0 && bracket == 0 {
				return
			}
		case '{':
			if paren != 0 || bracket != 0 {
				i++
				continue
			}
			selector := cssSelectorText(src, segStart, i)
			close := cssMatchBrace(mask, i, hi)
			contentEnd := close
			if contentEnd > hi {
				contentEnd = hi
			}
			line := cssSelectorLine(mask, starts, segStart, i)
			if strings.HasPrefix(selector, "@") {
				name := cssAtRuleName(selector)
				if cssContainerAtRules[name] {
					parseCSSBlock(src, mask, starts, i+1, contentEnd, depth+1, rules)
				} else if selector != "" && !cssHasTopLevelBrace(mask, i+1, contentEnd) {
					decls := cssDeclarations(src, mask, i+1, contentEnd)
					*rules = append(*rules, CSSRule{Selector: selector, Line: line, Declarations: decls})
				}
			} else if selector != "" {
				decls, nested := cssRuleBody(src, mask, starts, i+1, contentEnd, selector, line, depth, rules)
				_ = nested
				*rules = append(*rules, CSSRule{Selector: selector, Line: line, Declarations: decls})
			}
			segStart = contentEnd + 1
			i = contentEnd + 1
			continue
		}
		i++
	}
}

// cssRuleBody collects direct declarations of a rule and recursively records
// nested selectors (SCSS/LESS).
func cssRuleBody(src, mask []byte, starts []int, lo, hi int, parent string, parentLine, depth int, rules *[]CSSRule) ([]string, bool) {
	var decls []string
	cursor := lo
	nested := false
	i := lo
	paren, bracket := 0, 0
	for i < hi {
		switch mask[i] {
		case '(':
			paren++
		case ')':
			if paren > 0 {
				paren--
			}
		case '[':
			bracket++
		case ']':
			if bracket > 0 {
				bracket--
			}
		case ';':
			if paren == 0 && bracket == 0 {
				if d := strings.TrimSpace(string(src[cursor:i])); d != "" {
					decls = append(decls, d)
				}
				cursor = i + 1
			}
		case '{':
			if paren != 0 || bracket != 0 {
				i++
				continue
			}
			sel := cssSelectorText(src, cursor, i)
			close := cssMatchBrace(mask, i, hi)
			if sel != "" {
				nested = true
				if strings.HasPrefix(sel, "@") {
					name := cssAtRuleName(sel)
					if cssContainerAtRules[name] {
						parseCSSBlock(src, mask, starts, i+1, close, depth+1, rules)
					} else {
						childDecls := cssDeclarations(src, mask, i+1, close)
						line := cssSelectorLine(mask, starts, cursor, i)
						*rules = append(*rules, CSSRule{Selector: sel, Line: line, Declarations: childDecls})
					}
				} else {
					line := cssSelectorLine(mask, starts, cursor, i)
					childDecls, _ := cssRuleBody(src, mask, starts, i+1, close, sel, line, depth+1, rules)
					*rules = append(*rules, CSSRule{Selector: sel, Line: line, Declarations: childDecls})
				}
			}
			cursor = close + 1
			i = close + 1
			continue
		}
		i++
	}
	if cursor < hi {
		if d := strings.TrimSpace(string(src[cursor:hi])); d != "" {
			decls = append(decls, d)
		}
	}
	_ = parent
	_ = parentLine
	return decls, nested
}

func cssSelectorText(src []byte, lo, hi int) string {
	if lo < 0 {
		lo = 0
	}
	if hi > len(src) {
		hi = len(src)
	}
	var b strings.Builder
	for i := lo; i < hi; i++ {
		if src[i] == '/' && i+1 < hi && src[i+1] == '*' {
			end := bytes.Index(src[i+2:hi], []byte("*/"))
			if end < 0 {
				break
			}
			i += 2 + end + 1
			continue
		}
		b.WriteByte(src[i])
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func cssDeclarations(src, mask []byte, lo, hi int) []string {
	var out []string
	cursor := lo
	paren, bracket := 0, 0
	for i := lo; i < hi; i++ {
		switch mask[i] {
		case '(':
			paren++
		case ')':
			if paren > 0 {
				paren--
			}
		case '[':
			bracket++
		case ']':
			if bracket > 0 {
				bracket--
			}
		case ';':
			if paren == 0 && bracket == 0 {
				if d := strings.TrimSpace(string(src[cursor:i])); d != "" {
					out = append(out, d)
				}
				cursor = i + 1
			}
		}
	}
	if d := strings.TrimSpace(string(src[cursor:hi])); d != "" {
		out = append(out, d)
	}
	return out
}

func cssHasTopLevelBrace(mask []byte, lo, hi int) bool {
	depth := 0
	for i := lo; i < hi; i++ {
		switch mask[i] {
		case '{':
			return true
		case '}':
			if depth > 0 {
				depth--
			}
		}
	}
	return false
}

func cssMatchBrace(mask []byte, open, hi int) int {
	depth := 0
	for i := open; i < hi; i++ {
		switch mask[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return hi
}

func cssSelectorLine(mask []byte, starts []int, lo, hi int) int {
	for i := lo; i < hi && i < len(mask); i++ {
		if !isSpaceByte(mask[i]) {
			return lineAtOffset(starts, i)
		}
	}
	return lineAtOffset(starts, lo)
}

func cssAtRuleName(selector string) string {
	s := strings.TrimPrefix(selector, "@")
	if idx := strings.IndexAny(s, " \t(:{"); idx >= 0 {
		s = s[:idx]
	}
	return strings.ToLower(s)
}
