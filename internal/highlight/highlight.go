// Package highlight tokenizes source for the read-only code viewer. It is
// intentionally lightweight and dependency-free so the WebAssembly client can
// use the same tokenizer as the server.
package highlight

import "strings"

// Token is a colored byte range on one line. Start and End are byte offsets
// into the line, end-exclusive.
type Token struct {
	Line  int    `json:"line"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Class string `json:"class"`
}

type lexState struct {
	blockComment bool
	blockEnd     string // */ or -->
	stringDelim  string // ' """ ` etc. for multi-line strings
	rawString    bool
}

// Tokenize returns spans for lang. Unknown languages return nil (plain text).
func Tokenize(src, lang string) []Token {
	if lang == "html" || lang == "xml" {
		return tokenizeMarkup(src)
	}
	style, ok := styles[lang]
	if !ok {
		return nil
	}
	lines := strings.Split(src, "\n")
	var out []Token
	var st lexState
	for ln, line := range lines {
		out = append(out, tokenizeLine(line, ln, style, &st)...)
	}
	return out
}

type style struct {
	keywords   map[string]bool
	types      map[string]bool
	lineHash   bool
	lineSlash  bool
	blockSlash bool
	triple     bool
	backtick   bool
	upperType  bool
}

func mk(words string) map[string]bool {
	m := make(map[string]bool)
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

var styles = map[string]style{
	"go": {
		keywords:  mk(`break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var`),
		types:     mk(`bool byte complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr any comparable`),
		lineSlash: true, blockSlash: true, backtick: true,
	},
	"python": {
		keywords: mk(`and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield match case self`),
		types:    mk(`int float str bool bytes list dict set tuple object None True False`),
		lineHash: true, triple: true,
	},
	"javascript": {
		keywords:  mk(`async await break case catch class const continue debugger default delete do else export extends finally for from function get if import in instanceof let new of return set static super switch this throw try typeof var void while with yield`),
		types:     mk(`Array Boolean Date Error Function Map Number Object Promise RegExp Set String Symbol BigInt JSON Math console document window`),
		lineSlash: true, blockSlash: true, backtick: true,
	},
	"rust": {
		keywords:  mk(`as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while`),
		types:     mk(`i8 i16 i32 i64 i128 isize u8 u16 u32 u64 u128 usize f32 f64 bool char str String Vec Option Result Box`),
		lineSlash: true, blockSlash: true,
	},
	"c": {
		keywords:  mk(`auto break case char const continue default do double else enum extern float for goto if inline int long register restrict return short signed sizeof static struct switch typedef union unsigned void volatile while class namespace template public private protected virtual override new delete this using try catch throw constexpr noexcept nullptr true false`),
		types:     mk(`int8_t int16_t int32_t int64_t uint8_t uint16_t uint32_t uint64_t size_t bool string vector map`),
		lineSlash: true, blockSlash: true,
	},
	"java": {
		keywords:  mk(`abstract assert boolean break byte case catch char class const continue default do double else enum extends final finally float for goto if implements import instanceof int interface long native new package private protected public return short static strictfp super switch synchronized this throw throws transient try void volatile while var record sealed permits`),
		types:     mk(`String Integer Long Double Float Boolean Object List Map Set Optional`),
		lineSlash: true, blockSlash: true,
	},
	"css": {
		lineSlash: false, blockSlash: true,
	},
	"json": {
		lineSlash: true, blockSlash: true,
	},
	"markdown": {
		lineHash: true,
	},
	"yaml": {
		lineHash: true,
	},
	"toml": {
		lineHash: true,
	},
	"shell": {
		lineHash: true, backtick: true,
	},
	"sql": {
		keywords:  mk(`select from where insert update delete into values create table drop alter join left right inner outer on as group by order having limit distinct and or not null is in like case when then else end union all`),
		lineSlash: true, blockSlash: true,
	},
	"lua": {
		keywords: mk(`and break do else elseif end for function goto if in local not or repeat return then until while`),
		lineHash: true,
	},
	"ruby": {
		keywords: mk(`alias and begin break case class def defined do else elsif end ensure false for if in module next nil not or redo rescue retry return self super then true undef unless until when while yield`),
		lineHash: true,
	},
}

// Language aliases map editor language ids to a style.
func init() {
	styles["typescript"] = styles["javascript"]
	styles["javascriptreact"] = styles["javascript"]
	styles["typescriptreact"] = styles["javascript"]
	styles["cpp"] = styles["c"]
	styles["scss"] = styles["css"]
	styles["less"] = styles["css"]
	styles["jsonc"] = styles["json"]
	styles["bash"] = styles["shell"]
	styles["zsh"] = styles["shell"]
}

func tokenizeLine(s string, ln int, st style, state *lexState) []Token {
	var out []Token
	add := func(start, end int, class string) {
		if end > start {
			out = append(out, Token{Line: ln, Start: start, End: end, Class: class})
		}
	}
	// Continue a multi-line string or block comment.
	if state.blockComment {
		if idx := strings.Index(s, state.blockEnd); idx >= 0 {
			end := idx + len(state.blockEnd)
			add(0, end, "com")
			state.blockComment = false
			return append(out, tokenizeCode(s[end:], end, ln, st, state)...)
		}
		add(0, len(s), "com")
		return out
	}
	if state.stringDelim != "" {
		if idx := findDelim(s, state.stringDelim, 0); idx >= 0 {
			end := idx + len(state.stringDelim)
			add(0, end, "str")
			state.stringDelim = ""
			return append(out, tokenizeCode(s[end:], end, ln, st, state)...)
		}
		add(0, len(s), "str")
		return out
	}
	return tokenizeCode(s, 0, ln, st, state)
}

func tokenizeCode(s string, base, ln int, st style, state *lexState) []Token {
	var out []Token
	add := func(start, end int, class string) {
		if end > start {
			out = append(out, Token{Line: ln, Start: base + start, End: base + end, Class: class})
		}
	}
	i := 0
	for i < len(s) {
		c := s[i]
		// Whitespace is left uncolored.
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			i++
			continue
		}
		// Comments.
		if st.lineHash && c == '#' {
			add(i, len(s), "com")
			return out
		}
		if st.lineSlash && c == '/' && i+1 < len(s) && s[i+1] == '/' {
			add(i, len(s), "com")
			return out
		}
		if st.blockSlash && c == '/' && i+1 < len(s) && s[i+1] == '*' {
			if idx := strings.Index(s[i+2:], "*/"); idx >= 0 {
				add(i, i+2+idx+2, "com")
				i = i + 2 + idx + 2
				continue
			}
			state.blockComment = true
			state.blockEnd = "*/"
			add(i, len(s), "com")
			return out
		}
		if st.triple && (strings.HasPrefix(s[i:], `"""`) || strings.HasPrefix(s[i:], `'''`)) {
			delim := s[i : i+3]
			start := i
			i += 3
			if idx := strings.Index(s[i:], delim); idx >= 0 {
				add(start, i+idx+3, "str")
				i += idx + 3
			} else {
				add(start, len(s), "str")
				state.stringDelim = delim
				return out
			}
			continue
		}
		// Strings.
		if c == '"' || c == '\'' || (c == '`' && st.backtick) {
			delim := string(c)
			start := i
			i++
			closed := false
			for i < len(s) {
				if s[i] == '\\' {
					i += 2
					continue
				}
				if s[i] == c {
					i++
					closed = true
					break
				}
				i++
			}
			add(start, i, "str")
			if !closed {
				state.stringDelim = delim
			}
			continue
		}
		// Numbers.
		if c >= '0' && c <= '9' || (c == '.' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9') {
			start := i
			for i < len(s) {
				d := s[i]
				if d >= '0' && d <= '9' || d >= 'a' && d <= 'f' || d >= 'A' && d <= 'F' ||
					d == 'x' || d == 'X' || d == '.' || d == '_' || d == 'e' || d == 'E' || d == '+' || d == '-' {
					i++
					continue
				}
				break
			}
			add(start, i, "num")
			continue
		}
		// Identifiers.
		if isIdentStart(c) {
			start := i
			for i < len(s) && isIdentPart(s[i]) {
				i++
			}
			word := s[start:i]
			class := ""
			if st.keywords[word] {
				class = "kw"
			} else if st.types[word] {
				class = "type"
			} else {
				j := i
				for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
					j++
				}
				if j < len(s) && s[j] == '(' {
					class = "fn"
				} else if st.upperType && len(word) > 0 && word[0] >= 'A' && word[0] <= 'Z' {
					class = "type"
				}
			}
			if class != "" {
				add(start, i, class)
			}
			continue
		}
		// Operators and punctuation.
		if strings.ContainsRune("+-*/%=<>!&|^~?:", rune(c)) {
			start := i
			i++
			for i < len(s) && strings.ContainsRune("+-*/%=<>!&|^~?:", rune(s[i])) {
				i++
			}
			add(start, i, "op")
			continue
		}
		if strings.ContainsRune("(){}[];,", rune(c)) {
			add(i, i+1, "punct")
		}
		i++
	}
	return out
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9'
}

func findDelim(s, delim string, from int) int {
	if from >= len(s) {
		return -1
	}
	idx := strings.Index(s[from:], delim)
	if idx < 0 {
		return -1
	}
	return from + idx
}

// tokenizeMarkup highlights HTML/XML tags, attribute names and strings.
func tokenizeMarkup(src string) []Token {
	lines := strings.Split(src, "\n")
	var out []Token
	inComment := false
	for ln, line := range lines {
		i := 0
		for i < len(line) {
			if inComment {
				idx := strings.Index(line[i:], "-->")
				if idx < 0 {
					out = append(out, Token{Line: ln, Start: i, End: len(line), Class: "com"})
					break
				}
				end := i + idx + 3
				out = append(out, Token{Line: ln, Start: i, End: end, Class: "com"})
				i = end
				inComment = false
				continue
			}
			if strings.HasPrefix(line[i:], "<!--") {
				idx := strings.Index(line[i+4:], "-->")
				if idx < 0 {
					out = append(out, Token{Line: ln, Start: i, End: len(line), Class: "com"})
					inComment = true
					break
				}
				end := i + 4 + idx + 3
				out = append(out, Token{Line: ln, Start: i, End: end, Class: "com"})
				i = end
				continue
			}
			if line[i] != '<' {
				i++
				continue
			}
			// A tag: <name attr="v"> or </name> or <!doctype ...>
			start := i
			i++
			if i < len(line) && (line[i] == '/' || line[i] == '!') {
				i++
			}
			nameStart := i
			for i < len(line) && (isIdentPart(line[i]) || line[i] == '-' || line[i] == ':') {
				i++
			}
			if i > nameStart {
				out = append(out, Token{Line: ln, Start: nameStart, End: i, Class: "tag"})
			}
			for i < len(line) && line[i] != '>' {
				if line[i] == '"' || line[i] == '\'' {
					q := line[i]
					ss := i
					i++
					for i < len(line) && line[i] != q {
						i++
					}
					if i < len(line) {
						i++
					}
					out = append(out, Token{Line: ln, Start: ss, End: i, Class: "str"})
					continue
				}
				if isIdentStart(line[i]) {
					as := i
					for i < len(line) && (isIdentPart(line[i]) || line[i] == '-' || line[i] == ':') {
						i++
					}
					out = append(out, Token{Line: ln, Start: as, End: i, Class: "attr"})
					continue
				}
				i++
			}
			if i < len(line) && line[i] == '>' {
				i++
			}
			_ = start
		}
	}
	return out
}
