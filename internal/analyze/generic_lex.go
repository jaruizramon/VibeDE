package analyze

import (
	"bytes"
	"sort"
	"strings"
)

// lexStyle selects the comment/string syntax understood by maskSource.
type lexStyle int

const (
	lexPython  lexStyle = iota
	lexC                // C, C++, Java
	lexJS               // JavaScript, TypeScript (backtick templates)
	lexRust             // Rust (raw strings, lifetimes)
	lexCSS              // CSS (block comments, quoted strings)
	lexPreproc          // SCSS/LESS (CSS plus // line comments)
)

// maskSource returns a copy of src with comments and the contents of string
// literals replaced by spaces. Byte offsets are preserved exactly and
// newlines are kept, so masked source can be used for token scanning, brace
// matching and indentation while original offsets stay valid.
func maskSource(src []byte, style lexStyle) []byte {
	out := make([]byte, len(src))
	copy(out, src)
	blank := func(lo, hi int) {
		if lo < 0 {
			lo = 0
		}
		if hi > len(out) {
			hi = len(out)
		}
		for k := lo; k < hi; k++ {
			if out[k] != '\n' && out[k] != '\r' {
				out[k] = ' '
			}
		}
	}
	i := 0
	for i < len(src) {
		c := src[i]
		switch style {
		case lexPython:
			if c == '#' {
				j := i
				for j < len(src) && src[j] != '\n' {
					j++
				}
				blank(i, j)
				i = j
				continue
			}
			if c == '\'' || c == '"' {
				j := scanPythonString(src, i)
				blank(i, j)
				i = j
				continue
			}
		case lexCSS, lexPreproc:
			if c == '/' && i+1 < len(src) && src[i+1] == '*' {
				j := indexAfter(src, i+2, "*/")
				blank(i, j)
				i = j
				continue
			}
			if style == lexPreproc && c == '/' && i+1 < len(src) && src[i+1] == '/' {
				j := i
				for j < len(src) && src[j] != '\n' {
					j++
				}
				blank(i, j)
				i = j
				continue
			}
			if c == '\'' || c == '"' {
				j := scanQuoted(src, i, c, false)
				blank(i, j)
				i = j
				continue
			}
		case lexRust:
			if c == '/' && i+1 < len(src) {
				if src[i+1] == '/' {
					j := i
					for j < len(src) && src[j] != '\n' {
						j++
					}
					blank(i, j)
					i = j
					continue
				}
				if src[i+1] == '*' {
					j := scanRustBlockComment(src, i)
					blank(i, j)
					i = j
					continue
				}
			}
			if c == 'r' || c == 'b' || c == 'c' {
				if j, ok := scanRustRawString(src, i); ok {
					blank(i, j)
					i = j
					continue
				}
			}
			if c == '"' {
				j := scanQuoted(src, i, '"', true)
				blank(i, j)
				i = j
				continue
			}
			if c == '\'' {
				if j, ok := scanRustChar(src, i); ok {
					blank(i, j)
					i = j
					continue
				}
				// A lifetime such as 'static: keep the identifier visible.
			}
		case lexJS:
			if c == '/' && i+1 < len(src) {
				if src[i+1] == '/' {
					j := i
					for j < len(src) && src[j] != '\n' {
						j++
					}
					blank(i, j)
					i = j
					continue
				}
				if src[i+1] == '*' {
					j := indexAfter(src, i+2, "*/")
					blank(i, j)
					i = j
					continue
				}
				if regexStart(src, i) {
					if j, ok := scanJSRegex(src, i); ok {
						blank(i, j)
						i = j
						continue
					}
				}
			}
			if c == '`' {
				j := scanTemplateLiteral(src, i)
				blank(i, j)
				i = j
				continue
			}
			if c == '\'' || c == '"' {
				j := scanQuoted(src, i, c, false)
				blank(i, j)
				i = j
				continue
			}
		case lexC:
			if c == '/' && i+1 < len(src) {
				if src[i+1] == '/' {
					j := i
					for j < len(src) && src[j] != '\n' {
						j++
					}
					blank(i, j)
					i = j
					continue
				}
				if src[i+1] == '*' {
					j := indexAfter(src, i+2, "*/")
					blank(i, j)
					i = j
					continue
				}
			}
			if c == 'R' && i+1 < len(src) && src[i+1] == '"' && (i == 0 || !isIdentStart(src[i-1])) {
				if j, ok := scanCppRawString(src, i); ok {
					blank(i, j)
					i = j
					continue
				}
			}
			if c == '"' && i+2 < len(src) && src[i+1] == '"' && src[i+2] == '"' {
				j := indexAfter(src, i+3, `"""`)
				blank(i, j)
				i = j
				continue
			}
			if c == '\'' || c == '"' {
				j := scanQuoted(src, i, c, false)
				blank(i, j)
				i = j
				continue
			}
		}
		i++
	}
	return out
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\v' || b == '\f'
}

func isDigitByte(b byte) bool { return b >= '0' && b <= '9' }

func isIdentStart(b byte) bool {
	return b == '_' || b == '$' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b >= 0x80
}

func isIdentByte(b byte) bool {
	return isIdentStart(b) || isDigitByte(b)
}

// scanQuoted returns the offset just past a quoted literal starting at i.
// Unterminated literals stop at the end of the line (or the file for
// allowNewline).
func scanQuoted(src []byte, i int, quote byte, allowNewline bool) int {
	j := i + 1
	for j < len(src) {
		c := src[j]
		if c == '\\' {
			j += 2
			continue
		}
		if c == quote {
			return j + 1
		}
		if c == '\n' && !allowNewline {
			return j
		}
		j++
	}
	if j > len(src) {
		j = len(src)
	}
	return j
}

func scanPythonString(src []byte, i int) int {
	q := src[i]
	if i+2 < len(src) && src[i+1] == q && src[i+2] == q {
		j := i + 3
		for j < len(src) {
			if src[j] == '\\' {
				j += 2
				continue
			}
			if src[j] == q && j+2 < len(src) && src[j+1] == q && src[j+2] == q {
				return j + 3
			}
			j++
		}
		return len(src)
	}
	return scanQuoted(src, i, q, false)
}

func indexAfter(src []byte, from int, needle string) int {
	if from >= len(src) {
		return len(src)
	}
	if idx := bytes.Index(src[from:], []byte(needle)); idx >= 0 {
		return from + idx + len(needle)
	}
	return len(src)
}

func scanRustBlockComment(src []byte, i int) int {
	depth := 0
	j := i
	for j < len(src) {
		if j+1 < len(src) && src[j] == '/' && src[j+1] == '*' {
			depth++
			j += 2
			continue
		}
		if j+1 < len(src) && src[j] == '*' && src[j+1] == '/' {
			depth--
			j += 2
			if depth == 0 {
				return j
			}
			continue
		}
		j++
	}
	return len(src)
}

func scanRustRawString(src []byte, i int) (int, bool) {
	j := i
	if j < len(src) && (src[j] == 'b' || src[j] == 'c') {
		j++
	}
	if j >= len(src) || src[j] != 'r' {
		return 0, false
	}
	if i > 0 && isIdentByte(src[i-1]) {
		return 0, false
	}
	j++
	hashes := 0
	for j < len(src) && src[j] == '#' {
		hashes++
		j++
	}
	if j >= len(src) || src[j] != '"' {
		return 0, false
	}
	j++
	for j < len(src) {
		if src[j] == '"' {
			k := j + 1
			n := 0
			for k < len(src) && src[k] == '#' && n < hashes {
				k++
				n++
			}
			if n == hashes {
				return k, true
			}
		}
		j++
	}
	return len(src), true
}

func scanRustChar(src []byte, i int) (int, bool) {
	j := i + 1
	if j >= len(src) {
		return i + 1, false
	}
	if src[j] == '\\' {
		k := j + 2
		for k < len(src) && src[k] != '\'' && src[k] != '\n' {
			k++
		}
		if k < len(src) && src[k] == '\'' {
			return k + 1, true
		}
		return i + 1, false
	}
	k := j
	for k < len(src) && k-j <= 8 && src[k] != '\n' {
		if src[k] == '\'' {
			return k + 1, true
		}
		if !isIdentByte(src[k]) && src[k] != ' ' {
			return i + 1, false
		}
		k++
	}
	return i + 1, false
}

func scanCppRawString(src []byte, i int) (int, bool) {
	j := i + 2
	start := j
	for j < len(src) && src[j] != '(' && j-start <= 16 {
		j++
	}
	if j >= len(src) || src[j] != '(' {
		return 0, false
	}
	delim := string(src[start:j])
	end := ")" + delim + `"`
	if idx := bytes.Index(src[j+1:], []byte(end)); idx >= 0 {
		return j + 1 + idx + len(end), true
	}
	return len(src), true
}

func scanTemplateLiteral(src []byte, i int) int {
	j := i + 1
	for j < len(src) {
		if src[j] == '\\' {
			j += 2
			continue
		}
		if src[j] == '`' {
			return j + 1
		}
		j++
	}
	return len(src)
}

// regexStart reports whether a slash at i plausibly begins a regular
// expression literal rather than a division operator.
func regexStart(src []byte, i int) bool {
	j := i - 1
	for j >= 0 && (src[j] == ' ' || src[j] == '\t') {
		j--
	}
	if j < 0 {
		return true
	}
	switch src[j] {
	case '=', '(', ',', ':', '[', '!', '&', '|', '?', '{', ';', '+', '-', '*', '%', '^', '~', '>', '\n':
		return true
	}
	if isIdentByte(src[j]) {
		k := j
		for k >= 0 && isIdentByte(src[k]) {
			k--
		}
		switch string(src[k+1 : j+1]) {
		case "return", "typeof", "case", "in", "of", "delete", "void",
			"instanceof", "new", "yield", "await", "do", "else":
			return true
		}
	}
	return false
}

func scanJSRegex(src []byte, i int) (int, bool) {
	j := i + 1
	inClass := false
	for j < len(src) {
		c := src[j]
		switch c {
		case '\\':
			j += 2
			continue
		case '\n':
			return 0, false
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				j++
				for j < len(src) && isIdentByte(src[j]) {
					j++
				}
				return j, true
			}
		}
		j++
	}
	return 0, false
}

// genToken is one lexical token of masked source.
type genToken struct {
	text string
	pos  int // byte offset in the original source
	line int // 1-based
	col  int // 0-based
}

// genSource is a parsed generic file: original and masked bytes, tokens,
// bracket matching and brace depths.
type genSource struct {
	path string
	lang string
	src  []byte
	mask []byte

	directives bool
	lineStart  []int
	toks       []genToken
	match      []int
	depth      []int // brace depth before the token
	depthEnd   []int // brace depth after the token
	parenDepth []int // parenthesis depth before the token
}

func newGenSource(path, lang string, src []byte, style lexStyle) *genSource {
	s := &genSource{
		path:       path,
		lang:       lang,
		src:        src,
		mask:       maskSource(src, style),
		directives: style == lexC,
	}
	start := 0
	s.lineStart = append(s.lineStart, 0)
	for i, b := range src {
		if b == '\n' {
			start = i + 1
			s.lineStart = append(s.lineStart, start)
		}
	}
	s.lex()
	s.computeMatches()
	s.computeDepths()
	return s
}

func (s *genSource) lex() {
	mask := s.mask
	line := 1
	lineStart := 0
	i := 0
	for i < len(mask) {
		c := mask[i]
		if c == '\n' {
			line++
			lineStart = i + 1
			i++
			continue
		}
		if isSpaceByte(c) {
			i++
			continue
		}
		t := genToken{pos: i, line: line, col: i - lineStart}
		if isIdentStart(c) {
			j := i + 1
			for j < len(mask) && isIdentByte(mask[j]) {
				j++
			}
			t.text = string(mask[i:j])
			s.toks = append(s.toks, t)
			i = j
			continue
		}
		if isDigitByte(c) {
			j := i + 1
			for j < len(mask) {
				b := mask[j]
				if isIdentByte(b) {
					j++
					continue
				}
				if b == '.' && j+1 < len(mask) && isDigitByte(mask[j+1]) {
					j++
					continue
				}
				break
			}
			t.text = string(mask[i:j])
			s.toks = append(s.toks, t)
			i = j
			continue
		}
		n := genPunctLen(mask[i:])
		t.text = string(mask[i : i+n])
		s.toks = append(s.toks, t)
		i += n
	}
}

var genPuncts = []string{
	">>>=", "<<=", ">>=", "===", "!==", "**=", "//=", "..=", "...",
	">>>", "=>", "->", "::", "==", "!=", "<=", ">=", "&&", "||", "++", "--",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "??", "?.", "<<", ">>",
	"**", "//", "..", ":=",
}

func genPunctLen(b []byte) int {
	for _, p := range genPuncts {
		if len(b) >= len(p) && string(b[:len(p)]) == p {
			return len(p)
		}
	}
	return 1
}

func (s *genSource) computeMatches() {
	n := len(s.toks)
	s.match = make([]int, n)
	for i := range s.match {
		s.match[i] = -1
	}
	var paren, bracket, brace []int
	for i, t := range s.toks {
		switch t.text {
		case "(":
			paren = append(paren, i)
		case ")":
			if k := len(paren) - 1; k >= 0 {
				j := paren[k]
				paren = paren[:k]
				s.match[i] = j
				s.match[j] = i
			}
		case "[":
			bracket = append(bracket, i)
		case "]":
			if k := len(bracket) - 1; k >= 0 {
				j := bracket[k]
				bracket = bracket[:k]
				s.match[i] = j
				s.match[j] = i
			}
		case "{":
			brace = append(brace, i)
		case "}":
			if k := len(brace) - 1; k >= 0 {
				j := brace[k]
				brace = brace[:k]
				s.match[i] = j
				s.match[j] = i
			}
		}
	}
}

func (s *genSource) computeDepths() {
	n := len(s.toks)
	s.depth = make([]int, n)
	s.depthEnd = make([]int, n)
	s.parenDepth = make([]int, n)
	d := 0
	p := 0
	for i, t := range s.toks {
		s.depth[i] = d
		s.parenDepth[i] = p
		switch t.text {
		case "{":
			d++
		case "}":
			if d > 0 {
				d--
			}
		case "(":
			p++
		case ")":
			if p > 0 {
				p--
			}
		}
		s.depthEnd[i] = d
	}
}

func (s *genSource) lineCount() int {
	if len(s.src) == 0 {
		return 0
	}
	return len(s.lineStart)
}

// lineOf returns the 1-based line containing pos.
func (s *genSource) lineOf(pos int) int {
	if pos < 0 {
		pos = 0
	}
	if pos > len(s.src) {
		pos = len(s.src)
	}
	idx := sort.Search(len(s.lineStart), func(i int) bool { return s.lineStart[i] > pos }) - 1
	if idx < 0 {
		idx = 0
	}
	return idx + 1
}

func (s *genSource) colOf(pos int) int {
	line := s.lineOf(pos)
	start := s.lineStart[line-1]
	if pos < start {
		return 0
	}
	return pos - start
}

func (s *genSource) lineBounds(n int) (int, int) {
	if n < 1 || n > len(s.lineStart) {
		return 0, 0
	}
	start := s.lineStart[n-1]
	if start > len(s.src) {
		return len(s.src), len(s.src)
	}
	end := len(s.src)
	if n < len(s.lineStart) {
		end = s.lineStart[n] - 1
	}
	if end < start {
		end = start
	}
	for end > start && (s.src[end-1] == '\r' || s.src[end-1] == '\n') {
		end--
	}
	return start, end
}

func (s *genSource) lineString(n int) string {
	lo, hi := s.lineBounds(n)
	return string(s.src[lo:hi])
}

func (s *genSource) maskLineString(n int) string {
	lo, hi := s.lineBounds(n)
	if hi > len(s.mask) {
		hi = len(s.mask)
	}
	return string(s.mask[lo:hi])
}

func (s *genSource) lineIndent(n int) int {
	lo, hi := s.lineBounds(n)
	width := 0
	for k := lo; k < hi && k < len(s.mask); k++ {
		switch s.mask[k] {
		case ' ':
			width++
		case '\t':
			width += 4 - width%4
		default:
			return width
		}
	}
	return width
}

func (s *genSource) tokensIn(lo, hi int) []genToken {
	if lo < 0 {
		lo = 0
	}
	if hi >= len(s.toks) {
		hi = len(s.toks) - 1
	}
	if lo > hi {
		return nil
	}
	return s.toks[lo : hi+1]
}

// tokenIndexAtLine returns the index of the first token on the line inside
// [lo, hi], or -1.
func (s *genSource) firstTokenOnLine(line, lo, hi int) int {
	if lo < 0 {
		lo = 0
	}
	if hi >= len(s.toks) {
		hi = len(s.toks) - 1
	}
	for i := lo; i <= hi; i++ {
		if s.toks[i].line == line {
			return i
		}
		if s.toks[i].line > line {
			break
		}
	}
	return -1
}

func signatureFromSource(src []byte, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(src) {
		end = len(src)
	}
	if start >= end {
		return ""
	}
	s := strings.TrimSpace(string(src[start:end]))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}

func isIdentText(s string) bool {
	if s == "" || !isIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isIdentByte(s[i]) {
			return false
		}
	}
	return true
}

func isIntText(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigitByte(s[i]) {
			return false
		}
	}
	return true
}

// srcLineStarts returns the byte offset of the start of every line.
func srcLineStarts(src []byte) []int {
	starts := make([]int, 1, 1+len(src)/32)
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineAtOffset maps a byte offset to a 1-based line.
func lineAtOffset(starts []int, off int) int {
	if len(starts) == 0 {
		return 1
	}
	if off < 0 {
		off = 0
	}
	idx := sort.Search(len(starts), func(i int) bool { return starts[i] > off }) - 1
	if idx < 0 {
		idx = 0
	}
	return idx + 1
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// indexFold finds needle (lowercase ASCII) in src starting at from,
// case-insensitively.
func indexFold(src []byte, from int, needle string) int {
	if from < 0 {
		from = 0
	}
	n := len(needle)
	for i := from; i+n <= len(src); i++ {
		ok := true
		for j := 0; j < n; j++ {
			if lowerASCII(src[i+j]) != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
