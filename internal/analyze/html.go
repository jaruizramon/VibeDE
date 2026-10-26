package analyze

import "strings"

// analyzeHTML builds a tolerant element outline. It understands comments,
// doctypes, quoted attributes, self-closing tags, raw-text script/style
// elements and the common implicit end-tag rules. Template languages are
// treated as text.
func analyzeHTML(path string, src []byte) (Analysis, error) {
	a := Analysis{
		Path:      path,
		Language:  "html",
		Bytes:     int64(len(src)),
		Lines:     countLines(src),
		SLOC:      countSLOC(src),
		Functions: []Function{},
		Types:     []TypeInfo{},
	}
	a.HTML = parseHTMLOutline(src)
	computeMetrics(&a, nil)
	a.Notes = append(a.Notes,
		"HTML analysis is a tolerant tag outline; template expressions, embedded scripts and styles are not analyzed.")
	return a, nil
}

var htmlVoidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "command": true,
	"embed": true, "hr": true, "img": true, "input": true, "keygen": true,
	"link": true, "meta": true, "param": true, "source": true, "track": true,
	"wbr": true,
}

var htmlRawTextTags = map[string]bool{
	"script": true, "style": true,
}

func parseHTMLOutline(src []byte) []HTMLElement {
	starts := srcLineStarts(src)
	out := make([]HTMLElement, 0, 64)
	var stack []int
	i := 0
	for i < len(src) {
		if src[i] != '<' {
			j := i
			for j < len(src) && src[j] != '<' {
				j++
			}
			if len(stack) > 0 && j > i {
				top := stack[len(stack)-1]
				if out[top].Text == "" {
					out[top].Text = htmlShortText(string(src[i:j]))
				}
			}
			i = j
			continue
		}
		rest := src[i:]
		if len(rest) >= 4 && string(rest[:4]) == "<!--" {
			end := indexAfter(src, i+4, "-->")
			i = end
			continue
		}
		if len(rest) >= 9 && hasPrefixFold(rest, "<![cdata[") {
			end := indexAfter(src, i+9, "]]>")
			i = end
			continue
		}
		if len(rest) >= 2 && rest[1] == '!' {
			i = skipToGT(src, i+2)
			continue
		}
		if len(rest) >= 2 && rest[1] == '?' {
			i = skipToGT(src, i+2)
			continue
		}
		if len(rest) >= 2 && rest[1] == '/' {
			j := i + 2
			name, k := htmlTagName(src, j)
			if name != "" {
				for s := len(stack) - 1; s >= 0; s-- {
					if out[stack[s]].Tag == name {
						stack = stack[:s]
						break
					}
				}
			}
			i = skipToGT(src, k)
			continue
		}
		if len(rest) < 2 || !isHTMLNameStart(rest[1]) {
			// A stray '<' is text.
			i++
			continue
		}
		name, k := htmlTagName(src, i+1)
		if name == "" {
			i++
			continue
		}
		el := HTMLElement{Tag: name}
		selfClosing := false
		k, selfClosing = htmlAttributes(src, k, &el)
		autoCloseHTML(name, &stack, out)
		el.Line = lineAtOffset(starts, i)
		el.Depth = len(stack)
		out = append(out, el)
		if htmlRawTextTags[name] && !selfClosing {
			close := indexFold(src, k, "</"+name)
			if close < 0 {
				i = len(src)
				continue
			}
			i = close
			stack = append(stack, len(out)-1)
			continue
		}
		if selfClosing || htmlVoidTags[name] {
			i = k
			continue
		}
		stack = append(stack, len(out)-1)
		i = k
	}
	return out
}

func autoCloseHTML(tag string, stack *[]int, out []HTMLElement) {
	s := *stack
	for len(s) > 0 {
		top := out[s[len(s)-1]].Tag
		close := false
		switch tag {
		case "li":
			close = top == "li"
		case "p":
			close = top == "p"
		case "tr":
			close = top == "tr" || top == "td" || top == "th"
		case "td", "th":
			close = top == "td" || top == "th"
		case "option":
			close = top == "option"
		case "dt", "dd":
			close = top == "dt" || top == "dd"
		default:
			close = top == "p" && htmlBlockTags[tag]
		}
		if !close {
			break
		}
		s = s[:len(s)-1]
	}
	*stack = s
}

var htmlBlockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"div": true, "dl": true, "fieldset": true, "footer": true, "form": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"header": true, "hr": true, "main": true, "nav": true, "ol": true,
	"p": true, "pre": true, "section": true, "table": true, "ul": true,
}

func htmlTagName(src []byte, i int) (string, int) {
	start := i
	for i < len(src) && isHTMLNameByte(src[i]) {
		i++
	}
	if start == i {
		return "", i
	}
	return strings.ToLower(string(src[start:i])), i
}

func isHTMLNameStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isHTMLNameByte(b byte) bool {
	return isHTMLNameStart(b) || isDigitByte(b) || b == '-' || b == ':' || b == '_'
}

func htmlAttributes(src []byte, i int, el *HTMLElement) (int, bool) {
	selfClosing := false
	for i < len(src) {
		for i < len(src) && isSpaceByte(src[i]) {
			i++
		}
		if i >= len(src) {
			return i, selfClosing
		}
		if src[i] == '>' {
			return i + 1, selfClosing
		}
		if src[i] == '/' {
			selfClosing = true
			i++
			continue
		}
		nameStart := i
		for i < len(src) && !isSpaceByte(src[i]) && src[i] != '=' && src[i] != '>' && src[i] != '/' {
			i++
		}
		name := strings.ToLower(string(src[nameStart:i]))
		for i < len(src) && isSpaceByte(src[i]) {
			i++
		}
		value := ""
		if i < len(src) && src[i] == '=' {
			i++
			for i < len(src) && isSpaceByte(src[i]) {
				i++
			}
			if i < len(src) && (src[i] == '"' || src[i] == '\'') {
				q := src[i]
				i++
				vStart := i
				for i < len(src) && src[i] != q && src[i] != '>' {
					i++
				}
				value = string(src[vStart:i])
				if i < len(src) && src[i] == q {
					i++
				}
			} else {
				vStart := i
				for i < len(src) && !isSpaceByte(src[i]) && src[i] != '>' {
					i++
				}
				value = string(src[vStart:i])
			}
		}
		switch name {
		case "id":
			el.ID = value
		case "class":
			el.Classes = strings.Fields(value)
		}
	}
	return i, selfClosing
}

func htmlShortText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) > 80 {
		return string(runes[:80]) + "…"
	}
	return s
}

func skipToGT(src []byte, i int) int {
	for i < len(src) && src[i] != '>' {
		i++
	}
	if i < len(src) {
		return i + 1
	}
	return i
}

func hasPrefixFold(src []byte, prefix string) bool {
	if len(src) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if lowerASCII(src[i]) != prefix[i] {
			return false
		}
	}
	return true
}
