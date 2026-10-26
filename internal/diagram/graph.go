// Package diagram builds deterministic diagrams. All layout is computed in
// Go and emitted as SVG; DOT and Mermaid sources are provided for export.
package diagram

import (
	"fmt"
	"strings"
)

// NodeKind selects the shape and palette of a node.
type NodeKind string

const (
	Process  NodeKind = "process"
	Decision NodeKind = "decision"
	Terminal NodeKind = "terminal"
	Start    NodeKind = "start"
	End      NodeKind = "end"
	Class    NodeKind = "class"
	Actor    NodeKind = "actor"
	UseCase  NodeKind = "usecase"
	Note     NodeKind = "note"
	IO       NodeKind = "io"
)

// EdgeKind selects the line style and UML marker.
type EdgeKind string

const (
	Arrow       EdgeKind = "arrow"
	Inherit     EdgeKind = "inherit"
	Compose     EdgeKind = "compose"
	Aggregate   EdgeKind = "aggregate"
	Dependency  EdgeKind = "dependency"
	Association EdgeKind = "association"
)

// Section is a compartment in a class-style node.
type Section struct {
	Title string
	Lines []string
}

// Node is one diagram node. Label may contain newlines.
type Node struct {
	ID         string
	Label      string
	Stereotype string
	Kind       NodeKind
	Sections   []Section
	Group      string
}

// Edge connects two node IDs.
type Edge struct {
	From   string
	To     string
	Label  string
	Kind   EdgeKind
	Dashed bool
}

// Group draws a dashed boundary behind its member nodes.
type Group struct {
	ID    string
	Label string
}

// Graph is a deterministic diagram description.
type Graph struct {
	Title  string
	Nodes  []Node
	Edges  []Edge
	Groups []Group
}

// Doc is a rendered diagram plus portable sources.
type Doc struct {
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	SVG     string `json:"svg"`
	DOT     string `json:"dot"`
	Mermaid string `json:"mermaid"`
	Nodes   int    `json:"nodes"`
	Edges   int    `json:"edges"`
}

// Add appends a node. Empty IDs are ignored.
func (g *Graph) Add(n Node) {
	if n.ID == "" {
		return
	}
	g.Nodes = append(g.Nodes, n)
}

// Link appends an edge if both endpoints exist. Duplicate edges are kept only
// once (same endpoints, label and kind).
func (g *Graph) Link(e Edge) {
	if e.From == "" || e.To == "" {
		return
	}
	for _, old := range g.Edges {
		if old.From == e.From && old.To == e.To && old.Kind == e.Kind && old.Label == e.Label {
			return
		}
	}
	g.Edges = append(g.Edges, e)
}

// Node returns the node with id.
func (g *Graph) Node(id string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

// sanitizeID produces a stable Mermaid/DOT-safe identifier.
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 || (b.String()[0] >= '0' && b.String()[0] <= '9') {
		b.WriteString("n")
	}
	return b.String()
}

// DOT exports Graphviz DOT source.
func (g *Graph) DOT() string {
	var b strings.Builder
	b.WriteString("digraph vibede {\n")
	b.WriteString("  rankdir=TB;\n")
	b.WriteString("  bgcolor=\"transparent\";\n")
	b.WriteString("  node [shape=box, style=\"rounded,filled\", fillcolor=\"#182434\", color=\"#4b6484\", fontcolor=\"#dbe7f3\", fontname=\"monospace\", fontsize=11];\n")
	b.WriteString("  edge [color=\"#94a3b8\", fontcolor=\"#cbd5e1\", fontname=\"monospace\", fontsize=9];\n")
	ids := make(map[string]string, len(g.Nodes))
	for i, n := range g.Nodes {
		id := fmt.Sprintf("n%d", i)
		ids[n.ID] = id
		attrs := ""
		switch n.Kind {
		case Decision:
			attrs = ", shape=diamond"
		case Terminal, Start, End:
			attrs = ", shape=rect, style=\"rounded,filled\""
		case Class:
			attrs = ", shape=box, style=\"filled\""
			label := n.Label
			for _, s := range n.Sections {
				if s.Title != "" {
					label += "\\n-- " + s.Title + " --"
				}
				for _, ln := range s.Lines {
					label += "\\n" + ln
				}
			}
			fmt.Fprintf(&b, "  %s [label=\"%s\"%s];\n", id, dotEscape(label), attrs)
			continue
		case Actor:
			attrs = ", shape=plaintext"
		case UseCase:
			attrs = ", shape=ellipse"
		case Note:
			attrs = ", shape=note, style=\"filled,dashed\""
		}
		fmt.Fprintf(&b, "  %s [label=\"%s\"%s];\n", id, dotEscape(n.Label), attrs)
	}
	for _, e := range g.Edges {
		from, ok1 := ids[e.From]
		to, ok2 := ids[e.To]
		if !ok1 || !ok2 {
			continue
		}
		attrs := ""
		switch e.Kind {
		case Inherit:
			attrs = " [arrowhead=onormal]"
		case Compose:
			attrs = " [arrowtail=diamond, dir=both]"
		case Aggregate:
			attrs = " [arrowtail=odiamond, dir=both]"
		case Dependency:
			attrs = " [style=dashed]"
		case Association:
			attrs = " [arrowhead=none]"
		}
		if e.Dashed && e.Kind != Dependency {
			if strings.Contains(attrs, "style=") {
				attrs = strings.Replace(attrs, "style=dashed", "style=dashed", 1)
			} else {
				attrs += " [style=dashed]"
			}
		}
		if e.Label != "" {
			if attrs == "" {
				attrs = " [label=\"" + dotEscape(e.Label) + "\"]"
			} else {
				attrs = strings.TrimSuffix(attrs, "]") + ", label=\"" + dotEscape(e.Label) + "\"]"
			}
		}
		b.WriteString("  " + from + " -> " + to + attrs + ";\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func dotEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

// Mermaid exports a flowchart representation. Class sections are flattened
// into the label so the diagram is still readable without a custom renderer.
func (g *Graph) Mermaid() string {
	var b strings.Builder
	b.WriteString("flowchart TB\n")
	for i, n := range g.Nodes {
		id := fmt.Sprintf("n%d", i)
		label := mermaidEscape(n.Label)
		for _, s := range n.Sections {
			if s.Title != "" {
				label += "<br/><i>" + mermaidEscape(s.Title) + "</i>"
			}
			for _, ln := range s.Lines {
				label += "<br/>" + mermaidEscape(ln)
			}
		}
		switch n.Kind {
		case Decision:
			fmt.Fprintf(&b, "  %s{%q}\n", id, label)
		case Terminal, Start, End:
			fmt.Fprintf(&b, "  %s([%q])\n", id, label)
		case UseCase:
			fmt.Fprintf(&b, "  %s((%q))\n", id, label)
		case Actor:
			fmt.Fprintf(&b, "  %s[/%q/]\n", id, label)
		case Note:
			fmt.Fprintf(&b, "  %s>%q]\n", id, label)
		default:
			fmt.Fprintf(&b, "  %s[%q]\n", id, label)
		}
	}
	for _, e := range g.Edges {
		from := -1
		to := -1
		for i, n := range g.Nodes {
			if n.ID == e.From {
				from = i
			}
			if n.ID == e.To {
				to = i
			}
		}
		if from < 0 || to < 0 {
			continue
		}
		arrow := "-->"
		if e.Kind == Dependency || e.Dashed {
			arrow = "-.->"
		}
		if e.Kind == Association {
			arrow = "---"
		}
		label := ""
		if e.Label != "" {
			label = "|" + mermaidEscape(e.Label) + "|"
		}
		fmt.Fprintf(&b, "  n%d %s%s n%d\n", from, arrow, label, to)
	}
	return b.String()
}

func mermaidEscape(s string) string {
	s = strings.ReplaceAll(s, "\"", "'")
	s = strings.ReplaceAll(s, "\n", "<br/>")
	s = strings.ReplaceAll(s, "|", "/")
	return s
}
