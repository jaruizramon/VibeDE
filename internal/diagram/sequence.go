package diagram

import (
	"fmt"
	"strings"
)

// Participant is a sequence-diagram lane.
type Participant struct {
	ID    string
	Label string
	Actor bool
}

// Message is one arrow in a sequence diagram. Kind is sync, return or async.
type Message struct {
	From  string
	To    string
	Label string
	Kind  string
}

// Sequence is a deterministic sequence diagram description.
type Sequence struct {
	Title        string
	Participants []Participant
	Messages     []Message
}

// RenderSequence lays out and renders a sequence diagram.
func RenderSequence(s Sequence) Doc {
	title := s.Title
	if title == "" {
		title = "Sequence"
	}
	doc := Doc{Title: title, Kind: "sequence", DOT: sequenceDOT(s), Mermaid: sequenceMermaid(s), Nodes: len(s.Participants), Edges: len(s.Messages)}
	if len(s.Participants) == 0 {
		doc.SVG = emptySVG(title)
		return doc
	}
	type lane struct {
		p     Participant
		x, w  int
		known bool
	}
	lanes := make([]*lane, 0, len(s.Participants))
	byID := make(map[string]*lane, len(s.Participants))
	for _, p := range s.Participants {
		if p.ID == "" || byID[p.ID] != nil {
			continue
		}
		w := textW(p.Label) + 34
		if w < 118 {
			w = 118
		}
		l := &lane{p: p, w: w}
		lanes = append(lanes, l)
		byID[p.ID] = l
	}
	if len(lanes) == 0 {
		doc.SVG = emptySVG(title)
		return doc
	}
	const margin = 28
	x := margin
	for _, l := range lanes {
		l.x = x + l.w/2
		x += l.w + 34
	}
	width := x - 34 + margin
	if width < 420 {
		width = 420
	}
	msgStart := 126
	row := 46
	height := msgStart + len(s.Messages)*row + 32
	if height < 260 {
		height = 260
	}
	lifelineEnd := height - 28

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="%s" style="max-width:100%%;height:auto">`, width, height, width, height, esc(title))
	b.WriteString(`<defs>`)
	b.WriteString(marker("vb-seq-arrow", `<path d="M0,0 L10,5 L0,10 z" fill="`+colEdge+`"/>`, 9, 5, 8))
	b.WriteString(`</defs>`)
	b.WriteString(`<style>
text{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:12px;fill:` + colText + `}
.participant{fill:` + colNode + `;stroke:` + colStroke + `;stroke-width:1.2}
.lifeline{stroke:#334155;stroke-width:1;stroke-dasharray:4 5}
.msg{stroke:` + colEdge + `;stroke-width:1.35;fill:none}
.msg-label{font-size:11px;fill:#cbd5e1}
.title{font-size:14px;font-weight:600}
</style>`)
	fmt.Fprintf(&b, `<text x="%d" y="26" class="title">%s</text>`, margin, esc(title))
	for _, l := range lanes {
		fmt.Fprintf(&b, `<rect class="participant" x="%d" y="46" width="%d" height="36" rx="6"/>`, l.x-l.w/2, l.w)
		fmt.Fprintf(&b, `<text x="%d" y="69" text-anchor="middle">%s</text>`, l.x, esc(l.p.Label))
		fmt.Fprintf(&b, `<line class="lifeline" x1="%d" y1="82" x2="%d" y2="%d"/>`, l.x, l.x, lifelineEnd)
	}
	for i, m := range s.Messages {
		from := byID[m.From]
		to := byID[m.To]
		if from == nil || to == nil {
			continue
		}
		y := msgStart + i*row
		dashed := ""
		if m.Kind == "return" {
			dashed = ` stroke-dasharray="5 4"`
		}
		if from == to {
			fmt.Fprintf(&b, `<path class="msg" d="M%d,%d C%d,%d %d,%d %d,%d"%s marker-end="url(#vb-seq-arrow)"/>`,
				from.x+4, y, from.x+44, y+2, from.x+44, y+22, from.x+4, y+22, dashed)
			fmt.Fprintf(&b, `<text x="%d" y="%d" class="msg-label">%s</text>`, from.x+52, y+16, esc(truncate(m.Label, 46)))
			continue
		}
		x1, x2 := from.x, to.x
		if x2 > x1 {
			x1 += 4
			x2 -= 4
		} else {
			x1 -= 4
			x2 += 4
		}
		fmt.Fprintf(&b, `<line class="msg" x1="%d" y1="%d" x2="%d" y2="%d"%s marker-end="url(#vb-seq-arrow)"/>`, x1, y, x2, y, dashed)
		label := truncate(m.Label, 58)
		lx := (from.x + to.x) / 2
		if lx < margin+4 {
			lx = margin + 4
		}
		fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="middle" class="msg-label">%s</text>`, lx, y-7, esc(label))
	}
	b.WriteString(`</svg>`)
	doc.SVG = b.String()
	return doc
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func sequenceMermaid(s Sequence) string {
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	for _, p := range s.Participants {
		id := sanitizeID(p.ID)
		fmt.Fprintf(&b, "  participant %s as %s\n", id, mermaidEscape(p.Label))
	}
	for _, m := range s.Messages {
		from, to := sanitizeID(m.From), sanitizeID(m.To)
		arrow := "->>"
		if m.Kind == "return" {
			arrow = "-->>"
		}
		fmt.Fprintf(&b, "  %s%s%s: %s\n", from, arrow, to, mermaidEscape(m.Label))
	}
	return b.String()
}

func sequenceDOT(s Sequence) string {
	var b strings.Builder
	b.WriteString("digraph sequence {\n  rankdir=LR;\n")
	for i, p := range s.Participants {
		fmt.Fprintf(&b, "  p%d [shape=box, label=\"%s\"];\n", i, dotEscape(p.Label))
	}
	idx := make(map[string]int, len(s.Participants))
	for i, p := range s.Participants {
		idx[p.ID] = i
	}
	for i, m := range s.Messages {
		from, ok1 := idx[m.From]
		to, ok2 := idx[m.To]
		if !ok1 || !ok2 {
			continue
		}
		style := ""
		if m.Kind == "return" {
			style = " [style=dashed]"
		}
		fmt.Fprintf(&b, "  p%d -> p%d [label=\"%d: %s\"]%s;\n", from, to, i+1, dotEscape(m.Label), style)
	}
	b.WriteString("}\n")
	return b.String()
}
