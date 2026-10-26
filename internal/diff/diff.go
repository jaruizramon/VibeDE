// Package diff computes line-oriented diffs for files changed by an agent.
package diff

import (
	"strings"
)

// Kind is the role of a line in a hunk. It marshals to the conventional
// unified-diff prefix (" ", "+", "-").
type Kind string

const (
	Equal  Kind = " "
	Add    Kind = "+"
	Delete Kind = "-"
)

// Line is one line in a unified hunk. OldNo/NewNo are 1-based; zero when the
// line does not exist on that side.
type Line struct {
	Kind  Kind   `json:"kind"`
	Text  string `json:"text"`
	OldNo int    `json:"oldNo,omitempty"`
	NewNo int    `json:"newNo,omitempty"`
}

// Hunk is a contiguous changed region with surrounding context.
type Hunk struct {
	OldStart int    `json:"oldStart"`
	OldLines int    `json:"oldLines"`
	NewStart int    `json:"newStart"`
	NewLines int    `json:"newLines"`
	Lines    []Line `json:"lines"`
}

// Stats counts changed lines.
type Stats struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// SplitLines splits text into lines without trailing newline characters. CRLF
// input has its carriage returns removed. A trailing newline does not produce
// an extra empty line.
func SplitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

type op struct {
	kind Kind
	a, b int // indexes into old/new; -1 when unused
}

// Compute returns hunks describing old -> new with three lines of context.
// complexityCap bounds Myers' edit distance; beyond it, an exact common
// prefix/suffix diff with one replaced middle is returned instead.
func Compute(oldText, newText []byte, complexityCap int) ([]Hunk, Stats) {
	a := SplitLines(oldText)
	b := SplitLines(newText)
	ops := myers(a, b, complexityCap)
	return hunks(a, b, ops, 3), stats(ops)
}

// Count returns only statistics, avoiding hunk construction.
func Count(oldText, newText []byte, complexityCap int) Stats {
	a := SplitLines(oldText)
	b := SplitLines(newText)
	return stats(myers(a, b, complexityCap))
}

func stats(ops []op) Stats {
	var st Stats
	for _, o := range ops {
		switch o.kind {
		case Add:
			st.Added++
		case Delete:
			st.Removed++
		}
	}
	return st
}

// myers trims the common prefix/suffix and runs Myers on the middle. Typical
// agent edits touch a small region of a large file, so this keeps both time
// and the trace buffer bounded.
func myers(a, b []string, complexityCap int) []op {
	n, m := len(a), len(b)
	p := 0
	for p < n && p < m && a[p] == b[p] {
		p++
	}
	s := 0
	for s < n-p && s < m-p && a[n-1-s] == b[m-1-s] {
		s++
	}
	if p == 0 && s == 0 {
		return myersCore(a, b, complexityCap)
	}
	core := myersCore(a[p:n-s], b[p:m-s], complexityCap)
	out := make([]op, 0, p+len(core)+s)
	for i := 0; i < p; i++ {
		out = append(out, op{kind: Equal, a: i, b: i})
	}
	for _, o := range core {
		if o.a >= 0 {
			o.a += p
		}
		if o.b >= 0 {
			o.b += p
		}
		out = append(out, o)
	}
	for i := 0; i < s; i++ {
		out = append(out, op{kind: Equal, a: n - s + i, b: m - s + i})
	}
	return out
}

// myersCore computes a shortest edit script. It returns a deterministic
// script: deletions are emitted before insertions at the same position. The
// trace budget bounds worst-case memory for large rewrites; beyond it the
// exact prefix/suffix replacement fallback is used.
func myersCore(a, b []string, complexityCap int) []op {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	if n == 0 {
		out := make([]op, 0, m)
		for j := range b {
			out = append(out, op{kind: Add, a: -1, b: j})
		}
		return out
	}
	if m == 0 {
		out := make([]op, 0, n)
		for i := range a {
			out = append(out, op{kind: Delete, a: i, b: -1})
		}
		return out
	}

	max := n + m
	off := max
	v := make([]int, 2*max+2)
	trace := make([][]int, 0, max+1)
	limit := max
	if complexityCap > 0 && limit > complexityCap {
		limit = complexityCap
	}
	// Each d retains a full copy of v. Keep that trace under a fixed budget
	// instead of allowing a wholesale rewrite of a large file to consume
	// gigabytes.
	const traceBudgetBytes = 64 << 20
	if intsPerRow := 2*max + 2; intsPerRow > 0 {
		if budgetD := traceBudgetBytes / (intsPerRow * 8); budgetD < limit {
			limit = budgetD
		}
	}
	if limit < 0 {
		limit = 0
	}
	found := false
	var dFound int
	for d := 0; d <= limit; d++ {
		vc := append([]int(nil), v...)
		trace = append(trace, vc)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				dFound = d
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return prefixSuffixReplace(a, b)
	}
	return backtrack(a, b, trace, dFound, off)
}

func backtrack(a, b []string, trace [][]int, d, off int) []op {
	x, y := len(a), len(b)
	rev := make([]op, 0, len(a)+len(b))
	for steps := d; steps > 0; steps-- {
		v := trace[steps]
		k := x - y
		var prevK int
		if k == -steps || (k != steps && v[off+k-1] < v[off+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[off+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			rev = append(rev, op{kind: Equal, a: x, b: y})
		}
		if x == prevX {
			y--
			rev = append(rev, op{kind: Add, a: -1, b: y})
		} else {
			x--
			rev = append(rev, op{kind: Delete, a: x, b: -1})
		}
	}
	for x > 0 && y > 0 {
		x--
		y--
		rev = append(rev, op{kind: Equal, a: x, b: y})
	}
	for x > 0 {
		x--
		rev = append(rev, op{kind: Delete, a: x, b: -1})
	}
	for y > 0 {
		y--
		rev = append(rev, op{kind: Add, a: -1, b: y})
	}
	out := make([]op, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out
}

// prefixSuffixReplace is the bounded fallback when the edit distance exceeds
// complexityCap. It preserves any identical prefix and suffix and reports the
// middle as a replacement.
func prefixSuffixReplace(a, b []string) []op {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	out := make([]op, 0, len(a)+len(b))
	for i := 0; i < p; i++ {
		out = append(out, op{kind: Equal, a: i, b: i})
	}
	for i := p; i < len(a)-s; i++ {
		out = append(out, op{kind: Delete, a: i, b: -1})
	}
	for j := p; j < len(b)-s; j++ {
		out = append(out, op{kind: Add, a: -1, b: j})
	}
	for i := 0; i < s; i++ {
		out = append(out, op{kind: Equal, a: len(a) - s + i, b: len(b) - s + i})
	}
	return out
}

func hunks(a, b []string, ops []op, context int) []Hunk {
	var out []Hunk
	i := 0
	for i < len(ops) {
		// Find next change.
		for i < len(ops) && ops[i].kind == Equal {
			i++
		}
		if i >= len(ops) {
			break
		}
		start := i - context
		if start < 0 {
			start = 0
		}
		end := i
		for end < len(ops) {
			if ops[end].kind != Equal {
				end++
				continue
			}
			// Up to 2*context equal lines may separate changes in one hunk.
			j := end
			for j < len(ops) && j-end < context*2 && ops[j].kind == Equal {
				j++
			}
			if j < len(ops) && ops[j].kind != Equal && j-end <= context*2 {
				end = j
				continue
			}
			break
		}
		stop := end + context
		if stop > len(ops) {
			stop = len(ops)
		}
		h := Hunk{}
		first := true
		oldCount, newCount := 0, 0
		for _, o := range ops[start:stop] {
			line := Line{Kind: o.kind}
			switch o.kind {
			case Equal:
				line.Text = a[o.a]
				line.OldNo, line.NewNo = o.a+1, o.b+1
				oldCount++
				newCount++
			case Delete:
				line.Text = a[o.a]
				line.OldNo = o.a + 1
				oldCount++
			case Add:
				line.Text = b[o.b]
				line.NewNo = o.b + 1
				newCount++
			}
			if first && o.kind != Add {
				h.OldStart = o.a + 1
			} else if first {
				h.OldStart = o.b + 1
			}
			if first && o.kind != Delete {
				h.NewStart = o.b + 1
			} else if first {
				h.NewStart = o.a + 1
			}
			first = false
			h.Lines = append(h.Lines, line)
		}
		h.OldLines = oldCount
		h.NewLines = newCount
		if h.OldLines == 0 {
			h.OldStart--
			if h.OldStart < 0 {
				h.OldStart = 0
			}
		}
		if h.NewLines == 0 {
			h.NewStart--
			if h.NewStart < 0 {
				h.NewStart = 0
			}
		}
		out = append(out, h)
		i = end
	}
	return out
}
