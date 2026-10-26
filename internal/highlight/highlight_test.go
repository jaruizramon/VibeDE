package highlight

import (
	"strings"
	"testing"
)

func classesFor(src, lang string) map[string][]string {
	out := make(map[string][]string)
	lines := strings.Split(src, "\n")
	for _, tok := range Tokenize(src, lang) {
		if tok.Line < 0 || tok.Line >= len(lines) {
			continue
		}
		line := lines[tok.Line]
		if tok.Start < 0 || tok.End > len(line) || tok.Start > tok.End {
			panic("token out of bounds")
		}
		out[tok.Class] = append(out[tok.Class], line[tok.Start:tok.End])
	}
	return out
}

func TestGoTokens(t *testing.T) {
	src := "package main\n\nfunc main() {\n\ts := \"hi\" // note\n\tif s != \"\" { println(s) }\n}\n"
	got := classesFor(src, "go")
	joined := func(class string) string { return strings.Join(got[class], "|") }
	if !strings.Contains(joined("kw"), "package") || !strings.Contains(joined("kw"), "func") || !strings.Contains(joined("kw"), "if") {
		t.Fatalf("keywords = %q", joined("kw"))
	}
	if !strings.Contains(joined("str"), `"hi"`) || !strings.Contains(joined("str"), `""`) {
		t.Fatalf("strings = %q", joined("str"))
	}
	if !strings.Contains(joined("com"), "// note") {
		t.Fatalf("comments = %q", joined("com"))
	}
	if !strings.Contains(joined("fn"), "main") || !strings.Contains(joined("fn"), "println") {
		t.Fatalf("functions = %q", joined("fn"))
	}
}

func TestPythonTripleString(t *testing.T) {
	src := "def f():\n    s = \"\"\"line one\n# not a comment\nline three\"\"\"\n# real comment\n    return s\n"
	got := classesFor(src, "python")
	if len(got["str"]) < 1 || !strings.Contains(strings.Join(got["str"], "|"), "# not a comment") {
		t.Fatalf("triple string = %q", got["str"])
	}
	if len(got["com"]) != 1 || !strings.Contains(got["com"][0], "real comment") {
		t.Fatalf("comments = %q", got["com"])
	}
	if len(got["kw"]) == 0 || got["kw"][0] != "def" {
		t.Fatalf("keywords = %q", got["kw"])
	}
}

func TestMarkupTokens(t *testing.T) {
	src := `<div class="box" id='x'>hello</div><!-- note -->`
	got := classesFor(src, "html")
	if len(got["tag"]) < 2 || got["tag"][0] != "div" || got["tag"][1] != "div" {
		t.Fatalf("tags = %q", got["tag"])
	}
	if len(got["attr"]) < 2 || got["attr"][0] != "class" {
		t.Fatalf("attrs = %q", got["attr"])
	}
	if len(got["str"]) < 2 {
		t.Fatalf("strings = %q", got["str"])
	}
	if len(got["com"]) != 1 || !strings.Contains(got["com"][0], "note") {
		t.Fatalf("comments = %q", got["com"])
	}
}

func TestUnknownLanguageIsPlain(t *testing.T) {
	if toks := Tokenize("anything", "unknown"); len(toks) != 0 {
		t.Fatalf("tokens = %v", toks)
	}
}
