package diff

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
)

func TestComputeSimple(t *testing.T) {
	hunks, stats := Compute([]byte("a\nb\nc\n"), []byte("a\nB\nc\n"), 1000)
	if stats.Added != 1 || stats.Removed != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(hunks))
	}
	var adds, dels int
	for _, ln := range hunks[0].Lines {
		switch ln.Kind {
		case Add:
			adds++
			if ln.Text != "B" {
				t.Fatalf("added %q", ln.Text)
			}
		case Delete:
			dels++
			if ln.Text != "b" {
				t.Fatalf("deleted %q", ln.Text)
			}
		}
	}
	if adds != 1 || dels != 1 {
		t.Fatalf("hunk adds=%d dels=%d", adds, dels)
	}
}

func TestComputeIdenticalAndEmpty(t *testing.T) {
	if h, st := Compute([]byte("x\ny\n"), []byte("x\ny\n"), 100); len(h) != 0 || st != (Stats{}) {
		t.Fatalf("identical: hunks=%v stats=%+v", h, st)
	}
	h, st := Compute(nil, []byte("one\ntwo\n"), 100)
	if st.Added != 2 || st.Removed != 0 || len(h) != 1 {
		t.Fatalf("empty->content: hunks=%v stats=%+v", h, st)
	}
	h, st = Compute([]byte("one\ntwo\n"), nil, 100)
	if st.Added != 0 || st.Removed != 2 || len(h) != 1 {
		t.Fatalf("content->empty: hunks=%v stats=%+v", h, st)
	}
}

func TestSplitLinesTrailingAndCRLF(t *testing.T) {
	if got := SplitLines([]byte("a\r\nb\r\n")); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("SplitLines = %#v", got)
	}
	if got := SplitLines(nil); got != nil {
		t.Fatalf("empty = %#v", got)
	}
	if got := SplitLines([]byte("\n")); len(got) != 1 || got[0] != "" {
		t.Fatalf("newline = %#v", got)
	}
}

func TestDiffRoundTripRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	vocab := []string{"alpha", "beta", "gamma", "delta", "", "x = 1", "return"}
	for iter := 0; iter < 300; iter++ {
		oldN := rng.IntN(30)
		newN := rng.IntN(30)
		oldLines := make([]string, oldN)
		newLines := make([]string, newN)
		for i := range oldLines {
			oldLines[i] = vocab[rng.IntN(len(vocab))]
		}
		for i := range newLines {
			newLines[i] = vocab[rng.IntN(len(vocab))]
		}
		// Make the new file mostly related to the old one.
		copyN := oldN
		if newN < copyN {
			copyN = newN
		}
		for i := 0; i < copyN; i++ {
			if rng.IntN(10) != 0 {
				newLines[i] = oldLines[i]
			}
		}
		oldText := []byte(strings.Join(oldLines, "\n"))
		newText := []byte(strings.Join(newLines, "\n"))
		if len(oldText) > 0 {
			oldText = append(oldText, '\n')
		}
		if len(newText) > 0 {
			newText = append(newText, '\n')
		}
		hunks, stats := Compute(oldText, newText, 1000)
		got := applyHunks(SplitLines(oldText), hunks)
		want := SplitLines(newText)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("iter %d round trip failed\nold=%q\nnew=%q\ngot=%q", iter, oldText, newText, got)
		}
		if stats.Added != countKind(hunks, Add) || stats.Removed != countKind(hunks, Delete) {
			t.Fatalf("iter %d stats mismatch: %+v", iter, stats)
		}
	}
}

func applyHunks(old []string, hunks []Hunk) []string {
	// Hunks are in ascending order and include context. Rebuild new lines by
	// honoring old-side line numbers.
	out := make([]string, 0, len(old)+8)
	cursor := 0
	for _, h := range hunks {
		start := h.OldStart - 1
		if h.OldLines == 0 {
			start = h.OldStart
		}
		if start < cursor {
			start = cursor
		}
		if start > len(old) {
			start = len(old)
		}
		out = append(out, old[cursor:start]...)
		for _, ln := range h.Lines {
			if ln.Kind == Add || ln.Kind == Equal {
				out = append(out, ln.Text)
			}
		}
		cursor = start + h.OldLines
	}
	out = append(out, old[cursor:]...)
	return out
}

func countKind(hunks []Hunk, kind Kind) int {
	n := 0
	for _, h := range hunks {
		for _, ln := range h.Lines {
			if ln.Kind == kind {
				n++
			}
		}
	}
	return n
}

func TestLargeFileSmallEditUsesBoundedTrace(t *testing.T) {
	var oldLines, newLines []string
	for i := 0; i < 30000; i++ {
		line := "line-" + strconv.Itoa(i)
		oldLines = append(oldLines, line)
		newLines = append(newLines, line)
	}
	newLines[18000] = "line-changed"
	oldText := []byte(strings.Join(oldLines, "\n") + "\n")
	newText := []byte(strings.Join(newLines, "\n") + "\n")
	hunks, stats := Compute(oldText, newText, 3000)
	if stats.Added != 1 || stats.Removed != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	got := applyHunks(SplitLines(oldText), hunks)
	if strings.Join(got, "\n") != strings.Join(SplitLines(newText), "\n") {
		t.Fatal("large-file round trip failed")
	}
}

func TestLargeRewriteFallsBackAndRoundTrips(t *testing.T) {
	var oldLines, newLines []string
	for i := 0; i < 7000; i++ {
		oldLines = append(oldLines, "old-"+strconv.Itoa(i))
		newLines = append(newLines, "new-"+strconv.Itoa(i))
	}
	oldText := []byte(strings.Join(oldLines, "\n") + "\n")
	newText := []byte(strings.Join(newLines, "\n") + "\n")
	hunks, stats := Compute(oldText, newText, 3000)
	if stats.Added == 0 && stats.Removed == 0 {
		t.Fatal("rewrite produced empty diff")
	}
	got := applyHunks(SplitLines(oldText), hunks)
	if strings.Join(got, "\n") != strings.Join(SplitLines(newText), "\n") {
		t.Fatal("rewrite round trip failed")
	}
}
