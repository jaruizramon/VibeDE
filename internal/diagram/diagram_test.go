package diagram

import (
	"strings"
	"testing"
)

func sampleGraph() *Graph {
	g := &Graph{
		Title: "Checkout <flow>",
		Nodes: []Node{
			{ID: "start", Label: "start", Kind: Start},
			{ID: "check", Label: `if total > 100 && user != ""`, Kind: Decision},
			{ID: "class", Label: "Order", Kind: Class, Stereotype: "struct", Sections: []Section{
				{Title: "fields", Lines: []string{"ID string", "Total float64"}},
				{Title: "methods", Lines: []string{"Pay(ctx) error"}},
			}},
			{ID: "end", Label: "</svg><script>alert(1)</script>", Kind: End},
		},
		Edges: []Edge{
			{From: "start", To: "check"},
			{From: "check", To: "class", Label: "yes"},
			{From: "check", To: "end", Label: "no", Dashed: true},
		},
	}
	return g
}

func TestRenderDeterministicAndEscaped(t *testing.T) {
	g := sampleGraph()
	first := Render(g, "flowchart")
	second := Render(g, "flowchart")
	if first.SVG != second.SVG {
		t.Fatal("SVG is not deterministic")
	}
	if first.DOT != second.DOT || first.Mermaid != second.Mermaid {
		t.Fatal("export sources are not deterministic")
	}
	if strings.Contains(first.SVG, "<script>") {
		t.Fatal("SVG contains an unescaped script tag")
	}
	if !strings.Contains(first.SVG, "&lt;script&gt;") {
		t.Fatal("escaped label missing from SVG")
	}
	if !strings.Contains(first.SVG, "<svg") || !strings.Contains(first.SVG, "</svg>") {
		t.Fatal("SVG envelope missing")
	}
	if first.Nodes != 4 || first.Edges != 3 {
		t.Fatalf("counts = %d/%d", first.Nodes, first.Edges)
	}
	if !strings.Contains(first.Mermaid, "flowchart TB") {
		t.Fatalf("mermaid = %s", first.Mermaid)
	}
	if !strings.Contains(first.DOT, "digraph vibede") {
		t.Fatalf("dot = %s", first.DOT)
	}
}

func TestRenderEmpty(t *testing.T) {
	doc := Render(&Graph{Title: "Nothing"}, "class")
	if !strings.Contains(doc.SVG, "No diagram content") {
		t.Fatalf("svg = %s", doc.SVG)
	}
}

func TestSequenceDeterministic(t *testing.T) {
	seq := Sequence{
		Title: "Trace",
		Participants: []Participant{
			{ID: "main", Label: "main"},
			{ID: "parse", Label: "parse"},
			{ID: "store", Label: "store"},
		},
		Messages: []Message{
			{From: "main", To: "parse", Label: "parse(input)", Kind: "sync"},
			{From: "parse", To: "store", Label: "store(x)", Kind: "sync"},
			{From: "store", To: "parse", Label: "return", Kind: "return"},
		},
	}
	a := RenderSequence(seq)
	b := RenderSequence(seq)
	if a.SVG != b.SVG {
		t.Fatal("sequence SVG is not deterministic")
	}
	if !strings.Contains(a.SVG, "parse(input)") || !strings.Contains(a.SVG, "store(x)") {
		t.Fatal("messages missing")
	}
	if !strings.Contains(a.Mermaid, "sequenceDiagram") || !strings.Contains(a.DOT, "digraph sequence") {
		t.Fatalf("exports incomplete: %s / %s", a.Mermaid, a.DOT)
	}
}

func TestSelfAndBackEdgesRender(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			{ID: "a", Label: "a", Kind: Process},
			{ID: "b", Label: "b", Kind: Process},
		},
		Edges: []Edge{
			{From: "a", To: "a", Label: "self"},
			{From: "b", To: "a", Label: "back"},
		},
	}
	doc := Render(g, "flowchart")
	if doc.Edges != 2 {
		t.Fatalf("edges = %d", doc.Edges)
	}
	if !strings.Contains(doc.SVG, "<path") {
		t.Fatal("no path rendered")
	}
}
