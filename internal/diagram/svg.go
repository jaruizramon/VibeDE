package diagram

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	colBg      = "#0f172a"
	colNode    = "#182434"
	colNodeAlt = "#152033"
	colStroke  = "#4b6484"
	colText    = "#dbe7f3"
	colMuted   = "#93a4b8"
	colEdge    = "#94a3b8"
	colAccent  = "#7dd3fc"
)

// Render lays out g and returns SVG, DOT and Mermaid sources.
func Render(g *Graph, kind string) Doc {
	title := g.Title
	if title == "" {
		title = "Diagram"
	}
	doc := Doc{
		Title:   title,
		Kind:    kind,
		DOT:     g.DOT(),
		Mermaid: g.Mermaid(),
		Nodes:   len(g.Nodes),
		Edges:   len(g.Edges),
	}
	if len(g.Nodes) == 0 {
		doc.SVG = emptySVG(title)
		return doc
	}
	l := newLayout(g)
	doc.SVG = renderSVG(l, title)
	return doc
}

func emptySVG(title string) string {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="520" height="160" viewBox="0 0 520 160" role="img" aria-label="`)
	b.WriteString(esc(title))
	b.WriteString(`"><style>text{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;fill:` + colMuted + `;font-size:13px}</style>`)
	b.WriteString(`<rect x="1" y="1" width="518" height="158" rx="10" fill="none" stroke="#334155" stroke-dasharray="6 5"/>`)
	b.WriteString(`<text x="260" y="84" text-anchor="middle">No diagram content detected</text>`)
	b.WriteString(`</svg>`)
	return b.String()
}

func renderSVG(l *layout, title string) string {
	const off = 28
	w := l.width + off*2
	h := l.height + off*2
	if w < 240 {
		w = 240
	}
	if h < 120 {
		h = 120
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="%s" style="max-width:100%%;height:auto">`, w, h, w, h, esc(title))
	b.WriteString(`<defs>`)
	b.WriteString(marker("vb-arrow", `<path d="M0,0 L10,5 L0,10 z" fill="`+colEdge+`"/>`, 9, 5, 7))
	b.WriteString(marker("vb-tri", `<path d="M0,1 L10,5 L0,9 z" fill="`+colBg+`" stroke="`+colEdge+`" stroke-width="1.2"/>`, 9, 5, 10))
	b.WriteString(marker("vb-diamond-f", `<path d="M0,5 L5,0 L10,5 L5,10 z" fill="`+colEdge+`"/>`, 10, 5, 9))
	b.WriteString(marker("vb-diamond-o", `<path d="M0,5 L5,0 L10,5 L5,10 z" fill="`+colBg+`" stroke="`+colEdge+`" stroke-width="1.2"/>`, 10, 5, 9))
	b.WriteString(`</defs>`)
	b.WriteString(`<style>
text{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:12px;fill:` + colText + `}
.node-title{font-weight:600;font-size:13px}
.node-label{font-size:12px}
.stereotype{font-size:10px;font-style:italic;fill:` + colMuted + `}
.section-title{font-size:10px;fill:` + colMuted + `;letter-spacing:.08em}
.group-label{font-size:11px;fill:` + colMuted + `;letter-spacing:.08em}
.edge-label{font-size:10px;fill:#cbd5e1}
.edge{fill:none;stroke:` + colEdge + `;stroke-width:1.35}
.node{stroke-width:1.25}
</style>`)
	b.WriteString(`<g transform="translate(` + strconv.Itoa(off) + `,` + strconv.Itoa(off) + `)">`)
	// Groups behind everything.
	for _, g := range l.groups {
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="12" fill="#0b1220" fill-opacity=".6" stroke="#334155" stroke-dasharray="6 5"/>`, g.x, g.y, g.w, g.h)
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="group-label" text-transform="uppercase">%s</text>`, g.x+12, g.y+18, esc(strings.ToUpper(g.label)))
	}
	// Edges first so nodes cover their endpoints.
	for _, e := range l.edge {
		drawEdge(&b, e)
	}
	for _, n := range l.nodes {
		drawNode(&b, n)
	}
	b.WriteString(`</g></svg>`)
	return b.String()
}

func marker(id, body string, refX, refY, size int) string {
	return fmt.Sprintf(`<marker id="%s" viewBox="0 0 10 10" refX="%d" refY="%d" markerWidth="%d" markerHeight="%d" orient="auto-start-reverse" markerUnits="userSpaceOnUse">%s</marker>`,
		id, refX, refY, size, size, body)
}

type point struct{ x, y int }

func drawEdge(b *strings.Builder, e edgeInfo) {
	s, t := e.from, e.to
	if s == t {
		drawSelfEdge(b, e)
		return
	}
	var p0, c1, c2, p1 point
	sameRank := s.rank == t.rank
	switch {
	case sameRank:
		if t.x >= s.x {
			p0 = point{s.x + s.w, s.y + s.h/2}
			p1 = point{t.x, t.y + t.h/2}
			dx := p1.x - p0.x
			if dx < 50 {
				dx = 50
			}
			c1 = point{p0.x + dx/2, p0.y}
			c2 = point{p1.x - dx/2, p1.y}
		} else {
			p0 = point{s.x, s.y + s.h/2}
			p1 = point{t.x + t.w, t.y + t.h/2}
			dx := p0.x - p1.x
			if dx < 50 {
				dx = 50
			}
			c1 = point{p0.x - dx/2, p0.y}
			c2 = point{p1.x + dx/2, p1.y}
		}
	case t.rank > s.rank:
		p0 = point{s.x + s.w/2, s.y + s.h}
		p1 = point{t.x + t.w/2, t.y}
		dy := p1.y - p0.y
		if dy < 40 {
			dy = 40
		}
		c1 = point{p0.x, p0.y + dy/2}
		c2 = point{p1.x, p1.y - dy/2}
	default:
		p0 = point{s.x + s.w/2, s.y}
		p1 = point{t.x + t.w/2, t.y + t.h}
		dy := p0.y - p1.y
		if dy < 40 {
			dy = 40
		}
		c1 = point{p0.x, p0.y - dy/2}
		c2 = point{p1.x, p1.y + dy/2}
	}
	dashed := e.edge.Dashed || e.edge.Kind == Dependency || e.back
	dash := ""
	if dashed {
		dash = ` stroke-dasharray="5 4"`
	}
	markerEnd := ""
	markerStart := ""
	switch e.edge.Kind {
	case Inherit:
		markerEnd = ` marker-end="url(#vb-tri)"`
	case Compose:
		markerStart = ` marker-start="url(#vb-diamond-f)"`
		markerEnd = ` marker-end="url(#vb-arrow)"`
	case Aggregate:
		markerStart = ` marker-start="url(#vb-diamond-o)"`
		markerEnd = ` marker-end="url(#vb-arrow)"`
	case Association:
		// no arrowheads
	case Dependency:
		markerEnd = ` marker-end="url(#vb-arrow)"`
	default:
		markerEnd = ` marker-end="url(#vb-arrow)"`
	}
	fmt.Fprintf(b, `<path class="edge" d="M%d,%d C%d,%d %d,%d %d,%d"%s%s%s/>`,
		p0.x, p0.y, c1.x, c1.y, c2.x, c2.y, p1.x, p1.y, dash, markerEnd, markerStart)
	if e.edge.Label != "" {
		mx := (p0.x + 3*c1.x + 3*c2.x + p1.x) / 8
		my := (p0.y + 3*c1.y + 3*c2.y + p1.y) / 8
		tw := textW(e.edge.Label) + 10
		fmt.Fprintf(b, `<rect x="%d" y="%d" width="%d" height="16" rx="3" fill="`+colBg+`" fill-opacity=".92"/>`, mx-tw/2, my-9, tw)
		fmt.Fprintf(b, `<text x="%d" y="%d" text-anchor="middle" class="edge-label">%s</text>`, mx, my+4, esc(e.edge.Label))
	}
}

func drawSelfEdge(b *strings.Builder, e edgeInfo) {
	s := e.from
	y := s.y + s.h/2
	x := s.x + s.w
	dash := ""
	if e.edge.Dashed || e.edge.Kind == Dependency {
		dash = ` stroke-dasharray="5 4"`
	}
	marker := ` marker-end="url(#vb-arrow)"`
	if e.edge.Kind == Association {
		marker = ""
	}
	fmt.Fprintf(b, `<path class="edge" d="M%d,%d C%d,%d %d,%d %d,%d"%s%s/>`,
		x, y, x+48, y, x+48, y-38, x-2, y-28, dash, marker)
	if e.edge.Label != "" {
		fmt.Fprintf(b, `<text x="%d" y="%d" text-anchor="middle" class="edge-label">%s</text>`, x+30, y-32, esc(e.edge.Label))
	}
}

func drawNode(b *strings.Builder, n *sizeNode) {
	x, y, w, h := n.x, n.y, n.w, n.h
	kind := n.node.Kind
	labelAttr := esc(n.node.Label)
	switch kind {
	case Decision:
		cx, cy := x+w/2, y+h/2
		fmt.Fprintf(b, `<polygon class="node" points="%d,%d %d,%d %d,%d %d,%d" fill="#241f33" stroke="#7c6aa8"/>`, cx, y, x+w, cy, cx, y+h, x, cy)
		drawCentered(b, n.lines, cx, y+h/2, "node-label")
		b.WriteString(`<title>` + labelAttr + `</title>`)
		return
	case UseCase:
		cx, cy := x+w/2, y+h/2
		fmt.Fprintf(b, `<ellipse class="node" cx="%d" cy="%d" rx="%d" ry="%d" fill="#16283a" stroke="#4b7ba8"/>`, cx, cy, w/2, h/2)
		drawCentered(b, n.lines, cx, y+h/2, "node-label")
		b.WriteString(`<title>` + labelAttr + `</title>`)
		return
	case Actor:
		cx := x + w/2
		fmt.Fprintf(b, `<g class="node" stroke="#7aa2c8" fill="none" stroke-width="1.6"><circle cx="%d" cy="%d" r="9" fill="#16283a"/><line x1="%d" y1="%d" x2="%d" y2="%d"/><line x1="%d" y1="%d" x2="%d" y2="%d"/><line x1="%d" y1="%d" x2="%d" y2="%d"/><line x1="%d" y1="%d" x2="%d" y2="%d"/></g>`,
			cx, y+16,
			cx, y+25, cx, y+50,
			cx-13, y+34, cx+13, y+34,
			cx, y+50, cx-12, y+68,
			cx, y+50, cx+12, y+68,
		)
		fmt.Fprintf(b, `<text x="%d" y="%d" text-anchor="middle" class="node-label">`, cx, y+84)
		for i, line := range n.lines {
			if i == 0 {
				fmt.Fprintf(b, `<tspan x="%d">%s</tspan>`, cx, esc(line))
			} else {
				fmt.Fprintf(b, `<tspan x="%d" dy="15">%s</tspan>`, cx, esc(line))
			}
		}
		b.WriteString(`</text><title>` + labelAttr + `</title>`)
		return
	case Note:
		fmt.Fprintf(b, `<rect class="node" x="%d" y="%d" width="%d" height="%d" rx="6" fill="#26261a" stroke="#8a8450" stroke-dasharray="5 4"/>`, x, y, w, h)
		drawTopLeftLines(b, n.lines, x+padX, y+padY, "node-label")
		b.WriteString(`<title>` + labelAttr + `</title>`)
		return
	case IO:
		fmt.Fprintf(b, `<polygon class="node" points="%d,%d %d,%d %d,%d %d,%d" fill="#16283a" stroke="#4b7ba8"/>`, x+14, y, x+w, y, x+w-14, y+h, x, y+h)
		drawCentered(b, n.lines, x+w/2, y+h/2, "node-label")
		b.WriteString(`<title>` + labelAttr + `</title>`)
		return
	case Terminal, Start, End:
		fill, stroke := "#122f2a", "#2f8f7a"
		if kind == Start || kind == End {
			fill, stroke = "#132a3f", "#3b82b8"
		}
		fmt.Fprintf(b, `<rect class="node" x="%d" y="%d" width="%d" height="%d" rx="%d" fill="%s" stroke="%s"/>`, x, y, w, h, h/2, fill, stroke)
		drawCentered(b, n.lines, x+w/2, y+h/2, "node-label")
		b.WriteString(`<title>` + labelAttr + `</title>`)
		return
	case Class:
		drawClass(b, n)
		return
	default: // Process
		fmt.Fprintf(b, `<rect class="node" x="%d" y="%d" width="%d" height="%d" rx="7" fill="`+colNode+`" stroke="`+colStroke+`"/>`, x, y, w, h)
		drawCentered(b, n.lines, x+w/2, y+h/2, "node-label")
		b.WriteString(`<title>` + labelAttr + `</title>`)
	}
}

func drawClass(b *strings.Builder, n *sizeNode) {
	x, y, w, h := n.x, n.y, n.w, n.h
	fmt.Fprintf(b, `<rect class="node" x="%d" y="%d" width="%d" height="%d" rx="6" fill="`+colNodeAlt+`" stroke="#43618f"/>`, x, y, w, h)
	tx := x + padX
	cursor := y + 19
	if n.node.Stereotype != "" {
		fmt.Fprintf(b, `<text x="%d" y="%d" class="stereotype">«%s»</text>`, tx, cursor, esc(n.node.Stereotype))
		cursor += 15
	}
	first := true
	for _, line := range n.lines {
		cls := "node-title"
		if !first {
			cls = "node-label"
		}
		fmt.Fprintf(b, `<text x="%d" y="%d" class="%s">%s</text>`, tx, cursor, cls, esc(line))
		cursor += lineHeight
		first = false
	}
	cursor += 5
	for _, s := range n.sections {
		cursor += 6
		sep := cursor - 1
		fmt.Fprintf(b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#43618f" stroke-width="1"/>`, x, sep, x+w, sep)
		cursor += 13
		if s.title != "" {
			fmt.Fprintf(b, `<text x="%d" y="%d" class="section-title">%s</text>`, tx, cursor, esc(strings.ToUpper(s.title)))
			cursor += 15
		}
		for _, line := range s.lines {
			fmt.Fprintf(b, `<text x="%d" y="%d" class="node-label">%s</text>`, tx, cursor, esc(line))
			cursor += lineHeight
		}
	}
	fmt.Fprintf(b, `<title>%s</title>`, esc(n.node.Label))
}

func drawCentered(b *strings.Builder, lines []string, cx, cy int, class string) {
	if len(lines) == 0 {
		return
	}
	start := cy - (len(lines)-1)*lineHeight/2 + 4
	fmt.Fprintf(b, `<text x="%d" y="%d" text-anchor="middle" class="%s">`, cx, start, class)
	for i, line := range lines {
		if i == 0 {
			fmt.Fprintf(b, `<tspan x="%d">%s</tspan>`, cx, esc(line))
		} else {
			fmt.Fprintf(b, `<tspan x="%d" dy="%d">%s</tspan>`, cx, lineHeight, esc(line))
		}
	}
	b.WriteString(`</text>`)
}

func drawTopLeftLines(b *strings.Builder, lines []string, x, y int, class string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, `<text x="%d" y="%d" class="%s">`, x, y+11, class)
	for i, line := range lines {
		if i == 0 {
			fmt.Fprintf(b, `<tspan x="%d">%s</tspan>`, x, esc(line))
		} else {
			fmt.Fprintf(b, `<tspan x="%d" dy="%d">%s</tspan>`, x, lineHeight, esc(line))
		}
	}
	b.WriteString(`</text>`)
}

func esc(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}
