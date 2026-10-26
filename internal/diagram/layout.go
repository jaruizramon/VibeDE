package diagram

import (
	"sort"
	"strings"
)

const (
	fontSize   = 12
	lineHeight = 17
	gapX       = 42
	gapY       = 70
	padX       = 16
	padY       = 12
)

type sizeNode struct {
	node     *Node
	idx      int
	rank     int
	order    int
	x, y     int
	w, h     int
	lines    []string
	sections []sizeSection
	group    string
}

type sizeSection struct {
	title string
	lines []string
}

type layout struct {
	nodes  []*sizeNode
	byID   map[string]*sizeNode
	ranks  [][]*sizeNode
	groups []groupBox
	width  int
	height int
	edge   []edgeInfo
}

type groupBox struct {
	id         string
	label      string
	x, y, w, h int
}

type edgeInfo struct {
	edge Edge
	from *sizeNode
	to   *sizeNode
	back bool
}

func newLayout(g *Graph) *layout {
	l := &layout{byID: make(map[string]*sizeNode, len(g.Nodes))}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.ID == "" {
			continue
		}
		if _, dup := l.byID[n.ID]; dup {
			continue
		}
		sn := &sizeNode{node: n, idx: len(l.nodes), group: n.Group}
		sizeNodeBox(sn)
		l.nodes = append(l.nodes, sn)
		l.byID[n.ID] = sn
	}
	for _, e := range g.Edges {
		from := l.byID[e.From]
		to := l.byID[e.To]
		if from == nil || to == nil {
			continue
		}
		l.edge = append(l.edge, edgeInfo{edge: e, from: from, to: to})
	}
	l.detectBackEdges()
	l.assignRanks()
	l.orderRanks()
	l.place()
	l.placeGroups(g.Groups)
	l.normalize()
	return l
}

func sizeNodeBox(sn *sizeNode) {
	n := sn.node
	switch n.Kind {
	case Class:
		sn.sections = make([]sizeSection, 0, len(n.Sections))
		headerMax := 0
		for _, line := range wrapText(n.Label, 300) {
			if w := textW(line); w > headerMax {
				headerMax = w
			}
		}
		sn.lines = wrapText(n.Label, 300)
		maxW := headerMax
		if n.Stereotype != "" {
			if w := textW("«" + n.Stereotype + "»"); w > maxW {
				maxW = w
			}
		}
		for _, s := range n.Sections {
			ss := sizeSection{title: s.Title}
			for _, ln := range s.Lines {
				for _, wl := range wrapText(ln, 300) {
					ss.lines = append(ss.lines, wl)
					if w := textW(wl); w > maxW {
						maxW = w
					}
				}
			}
			if s.Title != "" {
				if w := textW(s.Title) + 12; w > maxW {
					maxW = w
				}
			}
			sn.sections = append(sn.sections, ss)
		}
		sn.w = maxW + 2*padX
		if sn.w < 170 {
			sn.w = 170
		}
		if sn.w > 380 {
			sn.w = 380
		}
		h := 16
		if n.Stereotype != "" {
			h += 14
		}
		h += len(sn.lines) * lineHeight
		h += 12
		if len(sn.sections) == 0 {
			h += 8
		}
		for _, s := range sn.sections {
			h += 8
			if s.title != "" {
				h += 15
			}
			h += len(s.lines) * lineHeight
		}
		sn.h = h + 10
	case Decision:
		sn.lines = wrapText(n.Label, 170)
		w := 0
		for _, line := range sn.lines {
			if tw := textW(line); tw > w {
				w = tw
			}
		}
		sn.w = w + 84
		if sn.w < 190 {
			sn.w = 190
		}
		sn.h = len(sn.lines)*lineHeight + 64
		if sn.h < 96 {
			sn.h = 96
		}
	case Actor:
		sn.lines = wrapText(n.Label, 130)
		w := 0
		for _, line := range sn.lines {
			if tw := textW(line); tw > w {
				w = tw
			}
		}
		sn.w = w + 24
		if sn.w < 84 {
			sn.w = 84
		}
		sn.h = 78 + len(sn.lines)*15
	case UseCase:
		sn.lines = wrapText(n.Label, 210)
		w := 0
		for _, line := range sn.lines {
			if tw := textW(line); tw > w {
				w = tw
			}
		}
		sn.w = w + 72
		if sn.w < 200 {
			sn.w = 200
		}
		sn.h = len(sn.lines)*lineHeight + 48
		if sn.h < 84 {
			sn.h = 84
		}
	case Note:
		sn.lines = wrapText(n.Label, 230)
		w := 0
		for _, line := range sn.lines {
			if tw := textW(line); tw > w {
				w = tw
			}
		}
		sn.w = w + 2*padX
		if sn.w < 180 {
			sn.w = 180
		}
		sn.h = len(sn.lines)*lineHeight + 2*padY
		if sn.h < 48 {
			sn.h = 48
		}
	case Terminal, Start, End:
		sn.lines = wrapText(n.Label, 240)
		w := 0
		for _, line := range sn.lines {
			if tw := textW(line); tw > w {
				w = tw
			}
		}
		sn.w = w + 48
		if sn.w < 110 {
			sn.w = 110
		}
		sn.h = 42
	case IO:
		sn.lines = wrapText(n.Label, 240)
		w := 0
		for _, line := range sn.lines {
			if tw := textW(line); tw > w {
				w = tw
			}
		}
		sn.w = w + 64
		if sn.w < 160 {
			sn.w = 160
		}
		sn.h = len(sn.lines)*lineHeight + 30
		if sn.h < 50 {
			sn.h = 50
		}
	default: // Process
		sn.lines = wrapText(n.Label, 230)
		w := 0
		for _, line := range sn.lines {
			if tw := textW(line); tw > w {
				w = tw
			}
		}
		sn.w = w + 2*padX
		if sn.w < 110 {
			sn.w = 110
		}
		sn.h = len(sn.lines)*lineHeight + 2*padY
		if sn.h < 44 {
			sn.h = 44
		}
	}
	if sn.w%2 != 0 {
		sn.w++
	}
	if sn.h%2 != 0 {
		sn.h++
	}
}

func textW(s string) int {
	w := 0
	for _, r := range s {
		if r > 0x2E80 {
			w += 12
		} else {
			w += 7
		}
	}
	return w
}

// wrapText splits a label into lines no wider than max px. It preserves
// explicit newlines and truncates very long output deterministically.
func wrapText(s string, max int) []string {
	if s == "" {
		return []string{""}
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		cur := ""
		flush := func() {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
		}
		for _, word := range words {
			for textW(word) > max && len(word) > 1 {
				cut := 1
				for cut < len(word) && textW(word[:cut+1]) <= max {
					cut++
				}
				if cur != "" {
					flush()
				}
				out = append(out, word[:cut])
				word = word[cut:]
			}
			cand := word
			if cur != "" {
				cand = cur + " " + word
			}
			if textW(cand) <= max {
				cur = cand
			} else {
				flush()
				cur = word
			}
		}
		flush()
	}
	if len(out) == 0 {
		out = []string{""}
	}
	if len(out) > 14 {
		out = append(out[:13], "…")
	}
	return out
}

func (l *layout) detectBackEdges() {
	state := make(map[*sizeNode]int, len(l.nodes)) // 0 unvisited, 1 stack, 2 done
	var visit func(n *sizeNode)
	visit = func(n *sizeNode) {
		state[n] = 1
		for i := range l.edge {
			e := &l.edge[i]
			if e.from != n {
				continue
			}
			switch state[e.to] {
			case 1:
				e.back = true
			case 0:
				visit(e.to)
			}
		}
		state[n] = 2
	}
	for _, n := range l.nodes {
		if state[n] == 0 {
			visit(n)
		}
	}
}

func (l *layout) assignRanks() {
	for _, n := range l.nodes {
		n.rank = 0
	}
	// Relaxation over the DAG (back edges excluded). n iterations is enough
	// for a DAG; cycles cannot exist after removal.
	for range len(l.nodes) {
		changed := false
		for _, e := range l.edge {
			if e.back {
				continue
			}
			if e.to.rank < e.from.rank+1 {
				e.to.rank = e.from.rank + 1
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	maxRank := 0
	for _, n := range l.nodes {
		if n.rank > maxRank {
			maxRank = n.rank
		}
	}
	l.ranks = make([][]*sizeNode, maxRank+1)
	for _, n := range l.nodes {
		l.ranks[n.rank] = append(l.ranks[n.rank], n)
	}
	for _, r := range l.ranks {
		sort.SliceStable(r, func(i, j int) bool { return r[i].idx < r[j].idx })
	}
}

func (l *layout) orderRanks() {
	if len(l.ranks) < 2 {
		return
	}
	pos := func(rank int) map[*sizeNode]int {
		m := make(map[*sizeNode]int, len(l.ranks[rank]))
		for i, n := range l.ranks[rank] {
			m[n] = i
		}
		return m
	}
	for sweep := 0; sweep < 4; sweep++ {
		if sweep%2 == 0 {
			for r := 1; r < len(l.ranks); r++ {
				prev := pos(r - 1)
				sortRank(l.ranks[r], func(n *sizeNode) float64 {
					sum, count := 0, 0
					for _, e := range l.edge {
						if e.to != n {
							continue
						}
						if p, ok := prev[e.from]; ok {
							sum += p
							count++
						}
					}
					if count == 0 {
						return float64(n.order)
					}
					return float64(sum) / float64(count)
				})
			}
		} else {
			for r := len(l.ranks) - 2; r >= 0; r-- {
				next := pos(r + 1)
				sortRank(l.ranks[r], func(n *sizeNode) float64 {
					sum, count := 0, 0
					for _, e := range l.edge {
						if e.from != n {
							continue
						}
						if p, ok := next[e.to]; ok {
							sum += p
							count++
						}
					}
					if count == 0 {
						return float64(n.order)
					}
					return float64(sum) / float64(count)
				})
			}
		}
		for r := range l.ranks {
			for i, n := range l.ranks[r] {
				n.order = i
			}
		}
	}
}

func sortRank(nodes []*sizeNode, bary func(*sizeNode) float64) {
	sort.SliceStable(nodes, func(i, j int) bool {
		bi, bj := bary(nodes[i]), bary(nodes[j])
		if bi != bj {
			return bi < bj
		}
		return nodes[i].idx < nodes[j].idx
	})
}

func (l *layout) place() {
	rankW := make([]int, len(l.ranks))
	rankH := make([]int, len(l.ranks))
	maxW := 0
	for r, nodes := range l.ranks {
		w := 0
		h := 0
		for i, n := range nodes {
			if i > 0 {
				w += gapX
			}
			w += n.w
			if n.h > h {
				h = n.h
			}
		}
		rankW[r] = w
		rankH[r] = h
		if w > maxW {
			maxW = w
		}
	}
	y := 0
	for r, nodes := range l.ranks {
		x := (maxW - rankW[r]) / 2
		for _, n := range nodes {
			n.x = x
			n.y = y
			x += n.w + gapX
		}
		y += rankH[r] + gapY
	}
	l.width = maxW
	l.height = y - gapY
}

func (l *layout) placeGroups(defs []Group) {
	for _, def := range defs {
		var members []*sizeNode
		for _, n := range l.nodes {
			if n.group == def.ID {
				members = append(members, n)
			}
		}
		if len(members) == 0 {
			continue
		}
		minX, minY := members[0].x, members[0].y
		maxX, maxY := 0, 0
		for _, n := range members {
			if n.x < minX {
				minX = n.x
			}
			if n.y < minY {
				minY = n.y
			}
			if n.x+n.w > maxX {
				maxX = n.x + n.w
			}
			if n.y+n.h > maxY {
				maxY = n.y + n.h
			}
		}
		l.groups = append(l.groups, groupBox{
			id:    def.ID,
			label: def.Label,
			x:     minX - 18,
			y:     minY - 30,
			w:     maxX - minX + 36,
			h:     maxY - minY + 48,
		})
	}
}

// normalize shifts everything into positive coordinates so group boundaries
// on the first rank are not clipped.
func (l *layout) normalize() {
	minX, minY := 0, 0
	for _, n := range l.nodes {
		if n.x < minX {
			minX = n.x
		}
		if n.y < minY {
			minY = n.y
		}
	}
	for _, g := range l.groups {
		if g.x < minX {
			minX = g.x
		}
		if g.y < minY {
			minY = g.y
		}
	}
	if minX < 0 {
		for _, n := range l.nodes {
			n.x -= minX
		}
		for i := range l.groups {
			l.groups[i].x -= minX
		}
		l.width -= minX
	}
	if minY < 0 {
		for _, n := range l.nodes {
			n.y -= minY
		}
		for i := range l.groups {
			l.groups[i].y -= minY
		}
		l.height -= minY
	}
	if l.width < 1 {
		l.width = 1
	}
	if l.height < 1 {
		l.height = 1
	}
}
