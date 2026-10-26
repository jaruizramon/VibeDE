//go:build js && wasm

package main

import (
	"fmt"
	"strings"
	"syscall/js"
	"unicode/utf8"
)

var retained []js.Func

func doc() js.Value { return js.Global().Get("document") }

func byID(id string) js.Value { return doc().Call("getElementById", id) }

func el(tag, class string) js.Value {
	e := doc().Call("createElement", tag)
	if class != "" {
		e.Set("className", class)
	}
	return e
}

func text(s string) js.Value { return doc().Call("createTextNode", s) }

func setText(e js.Value, s string) { e.Set("textContent", s) }

func add(parent js.Value, children ...js.Value) js.Value {
	for _, c := range children {
		parent.Call("appendChild", c)
	}
	return parent
}

func clear(e js.Value) {
	for {
		first := e.Get("firstChild")
		if first.IsNull() || first.IsUndefined() {
			return
		}
		e.Call("removeChild", first)
	}
}

func on(e js.Value, event string, fn func(js.Value)) {
	wrapped := js.FuncOf(func(_ js.Value, args []js.Value) any {
		var ev js.Value
		if len(args) > 0 {
			ev = args[0]
		}
		fn(ev)
		return nil
	})
	retained = append(retained, wrapped)
	e.Call("addEventListener", event, wrapped)
}

func setData(e js.Value, key, value string) {
	e.Get("dataset").Set(key, value)
}

func getData(e js.Value, key string) string {
	v := e.Get("dataset").Get(key)
	if v.IsUndefined() || v.IsNull() {
		return ""
	}
	return v.String()
}

func attr(e js.Value, key, value string) { e.Call("setAttribute", key, value) }

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string(r[:n-1]) + "…"
}

func compactNum(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func fileIcon(lang string) string {
	switch lang {
	case "go":
		return "GO"
	case "python":
		return "PY"
	case "javascript", "javascriptreact":
		return "JS"
	case "typescript", "typescriptreact":
		return "TS"
	case "rust":
		return "RS"
	case "c":
		return "C"
	case "cpp":
		return "C++"
	case "html":
		return "<>"
	case "css", "scss", "less":
		return "CSS"
	case "json", "jsonc":
		return "{}"
	case "markdown":
		return "MD"
	case "yaml":
		return "YM"
	case "shell":
		return "SH"
	case "java":
		return "JV"
	default:
		return "··"
	}
}

func sevClass(sev int) string {
	switch sev {
	case 1:
		return "sev-error"
	case 2:
		return "sev-warn"
	case 3:
		return "sev-info"
	default:
		return "sev-hint"
	}
}

func sevGlyph(sev int) string {
	switch sev {
	case 1:
		return "●"
	case 2:
		return "▲"
	case 3:
		return "◆"
	default:
		return "○"
	}
}

func langName(lang string) string {
	if lang == "" {
		return "plain text"
	}
	return lang
}

func joinArgs(args []string) string { return strings.Join(args, " ") }

var superscriptDigits = map[rune]string{
	'⁰': "0", '¹': "1", '²': "2", '³': "3", '⁴': "4",
	'⁵': "5", '⁶': "6", '⁷': "7", '⁸': "8", '⁹': "9",
	'ⁿ': "n",
}

// appendBigO renders Big-O notation with real <sup> elements so exponents are
// legible instead of relying on tiny Unicode superscript glyphs.
func appendBigO(parent js.Value, s string) {
	plain := 0
	flush := func(end int) {
		if end > plain {
			add(parent, text(s[plain:end]))
		}
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if digit, ok := superscriptDigits[r]; ok {
			flush(i)
			sup := el("sup", "")
			setText(sup, digit)
			add(parent, sup)
			i += size
			plain = i
			continue
		}
		if r == '^' {
			j := i + size
			start := j
			for j < len(s) {
				rr, sz := utf8.DecodeRuneInString(s[j:])
				if rr == ' ' || rr == ')' || rr == '·' || rr == ',' || rr == ';' {
					break
				}
				j += sz
			}
			if j > start {
				flush(i)
				sup := el("sup", "")
				setText(sup, s[start:j])
				add(parent, sup)
				i = j
				plain = j
				continue
			}
		}
		i += size
	}
	flush(len(s))
}

// bigOPlainUI is for tooltips and <option> text where HTML <sup> cannot be
// used. Caret notation keeps exponents at normal glyph size.
func bigOPlainUI(s string) string {
	r := strings.NewReplacer("⁰", "^0", "¹", "^1", "²", "^2", "³", "^3", "⁴", "^4",
		"⁵", "^5", "⁶", "^6", "⁷", "^7", "⁸", "^8", "⁹", "^9", "ⁿ", "^n")
	return r.Replace(s)
}
