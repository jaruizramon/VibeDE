package analyze

import (
	"strings"
	"testing"
)

func analyzeGoSource(t *testing.T, src string) Analysis {
	t.Helper()
	a, err := analyzeGo("sample.go", []byte(src))
	if err != nil {
		t.Fatalf("analyzeGo: %v", err)
	}
	return a
}

func functionMap(a Analysis) map[string]Function {
	out := make(map[string]Function, len(a.Functions))
	for _, f := range a.Functions {
		key := f.Name
		if f.Receiver != "" {
			key = f.Receiver + "." + f.Name
		}
		out[key] = f
	}
	return out
}

func TestGoBigOEstimates(t *testing.T) {
	src := `package main

import "sort"

func constant(n int) int {
	total := 0
	for i := 0; i < 10; i++ {
		total += i
	}
	return total
}

func linear(n int) {
	for range n {
	}
}

func quadratic(n int) {
	for i := range n {
		for j := range n {
			_ = i + j
		}
	}
}

func halving(n int) {
	for i := n; i > 0; i /= 2 {
	}
}

func binarySearch(xs []int, target int) int {
	lo, hi := 0, len(xs)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		if xs[mid] == target {
			return mid
		}
		if xs[mid] < target {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return -1
}

func sorting(xs []int) {
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
}

func helper(n int) {
	for range n {
	}
}

func composed(n int) {
	for range n {
		helper(n)
	}
}

func recursiveLinear(n int) int {
	if n <= 0 {
		return 0
	}
	return n + recursiveLinear(n-1)
}

func recursiveBinary(lo, hi int) int {
	if lo > hi {
		return -1
	}
	mid := (lo + hi) / 2
	if mid == 42 {
		return mid
	}
	return recursiveBinary(mid+1, hi)
}

func fibonacci(n int) int {
	if n < 2 {
		return n
	}
	return fibonacci(n-1) + fibonacci(n-2)
}
`
	a := analyzeGoSource(t, src)
	fns := functionMap(a)
	want := map[string]string{
		"constant":        "O(1)",
		"linear":          "O(n)",
		"quadratic":       "O(n²)",
		"halving":         "O(log n)",
		"binarySearch":    "O(log n)",
		"sorting":         "O(n log n)",
		"composed":        "O(n²)",
		"recursiveLinear": "O(n)",
		"recursiveBinary": "O(log n)",
		"fibonacci":       "O(2^n)",
	}
	for name, big := range want {
		fn, ok := fns[name]
		if !ok {
			t.Fatalf("missing function %s", name)
		}
		if fn.BigO != big {
			t.Errorf("%s BigO = %s (evidence %v), want %s", name, fn.BigO, fn.Evidence, big)
		}
	}
	if fns["fibonacci"].Complexity < 2 {
		t.Fatal("fibonacci should have branches")
	}
	if !strings.Contains(strings.Join(fns["quadratic"].Evidence, " "), "O(n)") {
		t.Fatalf("evidence = %v", fns["quadratic"].Evidence)
	}
	if a.Metrics.DominantBigO != "O(2^n)" {
		t.Fatalf("dominant = %s", a.Metrics.DominantBigO)
	}
	if a.Metrics.MaxComplexity <= 1 {
		t.Fatalf("max complexity = %d", a.Metrics.MaxComplexity)
	}
}

func TestGoTypesAndRelations(t *testing.T) {
	src := `package demo

type Reader interface {
	Read() error
}

type Inner struct {
	Value int
}

type Store struct {
	Inner
	Reader Reader
	Count int ` + "`json:\"count\"`" + `
}

func (s *Store) Read() error { return nil }

func (s Store) Close() {}
`
	a := analyzeGoSource(t, src)
	if len(a.Types) != 3 {
		t.Fatalf("types = %+v", a.Types)
	}
	var store, reader TypeInfo
	for _, ty := range a.Types {
		switch ty.Name {
		case "Store":
			store = ty
		case "Reader":
			reader = ty
		}
	}
	if store.Kind != "struct" || len(store.Methods) != 2 {
		t.Fatalf("store = %+v", store)
	}
	if len(store.Implements) != 1 || store.Implements[0] != "Reader" {
		t.Fatalf("implements = %v", store.Implements)
	}
	if len(reader.Methods) != 1 || reader.Methods[0].Name != "Read" {
		t.Fatalf("reader methods = %+v", reader.Methods)
	}
	if len(store.Fields) < 3 {
		t.Fatalf("fields = %+v", store.Fields)
	}
	foundTag := false
	for _, f := range store.Fields {
		if f.Name == "Count" && strings.Contains(f.Tag, "json") {
			foundTag = true
		}
	}
	if !foundTag {
		t.Fatal("struct tag not parsed")
	}
}

func TestGoParseErrorStillReturnsFileInfo(t *testing.T) {
	a, err := analyzeGo("broken.go", []byte("package main\nfunc broken( {\n"))
	if err != nil {
		t.Fatalf("partial parse should still return analysis: %v", err)
	}
	if a.Language != "go" || a.Bytes == 0 {
		t.Fatalf("analysis = %+v", a)
	}
	if len(a.Notes) == 0 || !strings.Contains(a.Notes[0], "parse error") {
		t.Fatalf("notes = %v", a.Notes)
	}
}

func TestGoConstantLoopDetection(t *testing.T) {
	a := analyzeGoSource(t, `package p

func bounded(n int) {
	for i := 0; i < 10; i++ {
		_ = i
	}
	for i := n; i > 0; i /= 2 {
		_ = i
	}
}
`)
	fn := functionMap(a)["bounded"]
	if fn.BigO != "O(1)" && fn.BigO != "O(log n)" {
		t.Fatalf("bounded BigO = %s", fn.BigO)
	}
	// The dominant of a constant loop and a logarithmic loop is log n.
	if fn.BigO != "O(log n)" {
		t.Fatalf("bounded BigO = %s, evidence %v", fn.BigO, fn.Evidence)
	}
}
