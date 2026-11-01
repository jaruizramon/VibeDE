package analyze

import (
	"reflect"
	"strings"
	"testing"
)

func mustAnalyze(t *testing.T, path, lang, src string) Analysis {
	t.Helper()
	a, err := analyzeGeneric(path, lang, []byte(src))
	if err != nil {
		t.Fatalf("analyzeGeneric(%s): %v", lang, err)
	}
	return a
}

func fnByKey(a Analysis, name, recv string) *Function {
	for i := range a.Functions {
		if a.Functions[i].Name == name && a.Functions[i].Receiver == recv {
			return &a.Functions[i]
		}
	}
	return nil
}

func typeByName(a Analysis, name string) *TypeInfo {
	for i := range a.Types {
		if a.Types[i].Name == name {
			return &a.Types[i]
		}
	}
	return nil
}

func hasEvidence(fn *Function, substr string) bool {
	for _, e := range fn.Evidence {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

func TestGenericPythonBigO(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "nested loops",
			src: `def sum_pairs(xs):
    total = 0
    for i in range(len(xs)):
        for j in range(len(xs)):
            total += xs[i] * xs[j]
    return total
`,
			want: "O(n²)",
		},
		{
			name: "binary search",
			src: `def search(a, target):
    lo, hi = 0, len(a)
    while lo < hi:
        mid = (lo + hi) // 2
        if a[mid] == target:
            return mid
        if a[mid] < target:
            lo = mid + 1
        else:
            hi = mid
    return -1
`,
			want: "O(log n)",
		},
		{
			name: "fibonacci recursion",
			src: `def fib(n):
    if n <= 1:
        return n
    return fib(n - 1) + fib(n - 2)
`,
			want: "O(2^n)",
		},
		{
			name: "constant range",
			src: `def first_ten(xs):
    total = 0
    for i in range(0, 10):
        total += xs[i]
    return total
`,
			want: "O(1)",
		},
		{
			name: "comprehension",
			src: `def pairs(xs):
    return [(x, y) for x in xs for y in xs]
`,
			want: "O(n²)",
		},
		{
			name: "recursion inside loop",
			src: `def walk(items):
    if not items:
        return 0
    total = 0
    for x in items:
        total += walk([x])
    return total
`,
			want: "O(n²)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := mustAnalyze(t, "case.py", "python", tc.src)
			if len(a.Functions) != 1 {
				t.Fatalf("functions = %d, want 1 (%+v)", len(a.Functions), a.Functions)
			}
			fn := &a.Functions[0]
			if fn.BigO != tc.want {
				t.Fatalf("%s BigO = %s, want %s (evidence %v)", fn.Name, fn.BigO, tc.want, fn.Evidence)
			}
		})
	}
}

func TestGenericTreeTraversalBigO(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		lang     string
		src      string
		want     string
		evidence string
	}{
		{
			name: "python children loop",
			file: "walk.py", lang: "python",
			src: `def walk(node):
    total = 0
    for child in node.children:
        total += walk(child)
    return total
`,
			want: "O(n)", evidence: "tree children",
		},
		{
			name: "python binary children tuple",
			file: "walk.py", lang: "python",
			src: `def walk(node):
    total = 0
    for child in (node.left, node.right):
        total += walk(child)
    return total
`,
			want: "O(n)", evidence: "tree children",
		},
		{
			name: "python linked list next",
			file: "list.py", lang: "python",
			src: `def walk(node):
    total = 0
    while node.next:
        total += walk(node.next)
        node = node.next
    return total
`,
			want: "O(n)", evidence: "tree children",
		},
		{
			name: "js children for-of",
			file: "walk.js", lang: "javascript",
			src: `function walk(node) {
  let total = 0;
  for (const child of node.children) {
    total += walk(child);
  }
  return total;
}
`,
			want: "O(n)", evidence: "tree children",
		},
		{
			name: "js children forEach",
			file: "walk.js", lang: "javascript",
			src: `function walk(node) {
  let total = 0;
  node.children.forEach(child => { total += walk(child); });
  return total;
}
`,
			want: "O(n)", evidence: "tree children",
		},
		{
			name: "real nested loop still multiplies",
			file: "walk.py", lang: "python",
			src: `def walk(node, xs):
    total = 0
    for child in node.children:
        for x in xs:
            total += walk(child) + x
    return total
`,
			want: "O(n²)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := mustAnalyze(t, tc.file, tc.lang, tc.src)
			if len(a.Functions) == 0 {
				t.Fatal("no functions analyzed")
			}
			fn := &a.Functions[0]
			if fn.BigO != tc.want {
				t.Fatalf("%s BigO = %s, want %s (evidence %v)", fn.Name, fn.BigO, tc.want, fn.Evidence)
			}
			if tc.evidence != "" && !hasEvidence(fn, tc.evidence) {
				t.Fatalf("%s evidence = %v, want substring %q", fn.Name, fn.Evidence, tc.evidence)
			}
		})
	}
}

func TestGenericPythonStructure(t *testing.T) {
	src := `import os
import sys as system
from collections import OrderedDict

class Greeter(Base, metaclass=Meta):
    """A greeting."""

    count: int = 0

    def __init__(self, name):
        self.name = name

    @property
    def label(self):
        return self.name
`
	a := mustAnalyze(t, "greeter.py", "python", src)
	if got := strings.Join(a.Imports, ","); got != "collections,os,sys" {
		t.Fatalf("imports = %q", got)
	}
	if len(a.Functions) != 2 {
		t.Fatalf("functions = %d: %+v", len(a.Functions), a.Functions)
	}
	init := fnByKey(a, "__init__", "Greeter")
	if init == nil || init.Kind != "method" {
		t.Fatalf("missing method __init__: %+v", a.Functions)
	}
	label := fnByKey(a, "label", "Greeter")
	if label == nil {
		t.Fatalf("missing method label: %+v", a.Functions)
	}
	if strings.Contains(label.Signature, "@property") || !strings.HasPrefix(label.Signature, "def label") {
		t.Fatalf("signature includes decorator: %q", label.Signature)
	}
	if label.StartLine != 14 || label.EndLine != 15 {
		t.Fatalf("label lines = %d-%d", label.StartLine, label.EndLine)
	}
	ti := typeByName(a, "Greeter")
	if ti == nil {
		t.Fatal("missing Greeter type")
	}
	if ti.Kind != "class" || len(ti.Bases) != 1 || ti.Bases[0] != "Base" {
		t.Fatalf("type = %+v", ti)
	}
	names := make([]string, 0, len(ti.Fields))
	for _, f := range ti.Fields {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "count,name" {
		t.Fatalf("fields = %v", names)
	}
	if got := ti.Fields[0].Type; got != "int" {
		t.Fatalf("count type = %q", got)
	}
	if len(ti.Methods) != 2 {
		t.Fatalf("methods = %+v", ti.Methods)
	}
}

func TestGenericJSBigOAndStructure(t *testing.T) {
	src := `import { readFile } from "fs";

export function helper(n: number): number {
  let s = 0;
  for (let i = 0; i < n; i++) { s += i; }
  return s;
}

export function outer(n: number): number {
  let t = 0;
  for (let i = 0; i < n; i++) {
    t += helper(n);
  }
  return t;
}

const add = (a: number, b: number) => a + b;

const render = (items: string[]) => {
  items.sort();
  return items.map(x => x);
};

class Repo {
  private items: string[] = [];
  add(x: string) { this.items.push(x); }
  get size() { return this.items.length; }
  sumAll(n: number) {
    let total = 0;
    for (let i = 0; i < n; i++) { total += i; }
    return total;
  }
  aggregate(n: number) {
    let total = 0;
    for (let i = 0; i < n; i++) { total += this.sumAll(n); }
    return total;
  }
}

interface Store {
  items: string[];
  load(id: string): Promise<void>;
  ready: () => boolean;
}

const handlers = {
  onClick: (e) => e.preventDefault(),
  double: (x) => x * 2,
};

type Handler = { run: (x: number) => void };
type Handlers = { first: (x: number) => void, second: (y: number) => void };
const nested = { inner: { one: (x) => x, two: (y) => y } };
`
	a := mustAnalyze(t, "sample.ts", "typescript", src)
	if got := strings.Join(a.Imports, ","); got != "fs" {
		t.Fatalf("imports = %q", got)
	}
	helper := fnByKey(a, "helper", "")
	if helper == nil || helper.BigO != "O(n)" {
		t.Fatalf("helper = %+v", helper)
	}
	if !helper.Exported {
		t.Fatal("helper should be exported")
	}
	outer := fnByKey(a, "outer", "")
	if outer == nil || outer.BigO != "O(n²)" {
		t.Fatalf("outer = %+v", outer)
	}
	if len(outer.Calls) != 1 || outer.Calls[0] != "helper" {
		t.Fatalf("outer calls = %v", outer.Calls)
	}
	add := fnByKey(a, "add", "")
	if add == nil || add.Kind != "arrow" {
		t.Fatalf("missing arrow add: %+v", a.Functions)
	}
	render := fnByKey(a, "render", "")
	if render == nil || render.BigO != "O(n log n)" {
		t.Fatalf("render = %+v", render)
	}
	if !hasEvidence(render, "sort call") {
		t.Fatalf("render evidence = %v", render.Evidence)
	}
	repo := typeByName(a, "Repo")
	if repo == nil || repo.Kind != "class" {
		t.Fatalf("Repo = %+v", repo)
	}
	if len(repo.Fields) != 1 || repo.Fields[0].Name != "items" || repo.Fields[0].Type != "string[]" {
		t.Fatalf("Repo fields = %+v", repo.Fields)
	}
	if fnByKey(a, "add", "Repo") == nil || fnByKey(a, "size", "Repo") == nil {
		t.Fatalf("React methods missing: %+v", a.Functions)
	}
	aggregate := fnByKey(a, "aggregate", "Repo")
	if aggregate == nil || aggregate.BigO != "O(n²)" {
		t.Fatalf("aggregate = %+v", aggregate)
	}
	if len(aggregate.Calls) != 1 || aggregate.Calls[0] != "sumAll" {
		t.Fatalf("aggregate calls = %v", aggregate.Calls)
	}
	onClick := fnByKey(a, "onClick", "")
	if onClick == nil || onClick.Kind != "arrow" || !strings.HasPrefix(onClick.Signature, "onClick:") {
		t.Fatalf("property arrow onClick = %+v", onClick)
	}
	if dbl := fnByKey(a, "double", ""); dbl == nil || dbl.Kind != "arrow" {
		t.Fatalf("property arrow double = %+v", dbl)
	}
	if fnByKey(a, "run", "") != nil {
		t.Fatalf("type alias produced a function: %+v", a.Functions)
	}
	if fnByKey(a, "first", "") != nil || fnByKey(a, "second", "") != nil {
		t.Fatalf("type literal produced functions: %+v", a.Functions)
	}
	if fnByKey(a, "one", "") == nil || fnByKey(a, "two", "") == nil {
		t.Fatalf("nested object arrows missing: %+v", a.Functions)
	}
	for _, f := range a.Functions {
		if f.Name == "x" || f.Name == "map" {
			t.Fatalf("anonymous callback extracted as function: %+v", f)
		}
	}
	if a.Metrics.DominantBigO != "O(n²)" {
		t.Fatalf("dominant big-o = %q", a.Metrics.DominantBigO)
	}
}

func TestGenericPythonDocstringOnly(t *testing.T) {
	src := `def documented():
    """Just a docstring."""
`
	a := mustAnalyze(t, "doc.py", "python", src)
	if len(a.Functions) != 1 {
		t.Fatalf("functions = %+v", a.Functions)
	}
	fn := &a.Functions[0]
	if fn.EndLine != 2 || fn.BigO != "O(1)" {
		t.Fatalf("fn = %+v", fn)
	}
	if fn.Signature != "def documented():" {
		t.Fatalf("signature = %q", fn.Signature)
	}
}

func TestGenericPythonAsync(t *testing.T) {
	src := `class Worker:
    async def run(self, n):
        for i in range(n):
            await self.step(i)

    async def step(self, i):
        return i
`
	a := mustAnalyze(t, "worker.py", "python", src)
	run := fnByKey(a, "run", "Worker")
	if run == nil || run.Kind != "method" {
		t.Fatalf("run = %+v", run)
	}
	if !strings.HasPrefix(run.Signature, "async def run") {
		t.Fatalf("signature = %q", run.Signature)
	}
	if run.BigO != "O(n)" {
		t.Fatalf("run BigO = %q", run.BigO)
	}
	if fnByKey(a, "step", "Worker") == nil {
		t.Fatalf("step missing: %+v", a.Functions)
	}
}

func TestGenericRustBigOAndStructure(t *testing.T) {
	src := `use std::collections::{HashMap, HashSet};
use crate::util::*;

pub struct Node {
    pub value: i32,
    next: Option<Box<Node>>,
}

pub trait Shape {
    fn area(&self) -> f64;
    fn name(&self) -> String { String::from("shape") }
}

impl Shape for Node {
    fn area(&self) -> f64 { 0.0 }
}

impl Node {
    pub fn new(value: i32) -> Self {
        Node { value, next: None }
    }

    pub fn count(&self, n: usize) -> usize {
        let mut total = 0;
        for i in 0..n {
            total += i;
        }
        total
    }
}

fn fib(n: u64) -> u64 {
    if n < 2 { n } else { fib(n - 1) + fib(n - 2) }
}

fn sort_all(v: &mut Vec<i32>) {
    v.sort();
    for i in 0..v.len() {
        for j in 0..v.len() {
            let _ = v[i] + v[j];
        }
    }
}
`
	a := mustAnalyze(t, "lib.rs", "rust", src)
	if got := strings.Join(a.Imports, ","); got != "crate::util::*,std::collections::HashMap,std::collections::HashSet" {
		t.Fatalf("imports = %q", got)
	}
	node := typeByName(a, "Node")
	if node == nil || !node.Exported {
		t.Fatalf("Node = %+v", node)
	}
	if len(node.Fields) != 2 || node.Fields[1].Name != "next" {
		t.Fatalf("Node fields = %+v", node.Fields)
	}
	if node.Fields[1].Type != "Option<Box<Node>>" {
		t.Fatalf("next type = %q", node.Fields[1].Type)
	}
	if len(node.Implements) != 1 || node.Implements[0] != "Shape" {
		t.Fatalf("Node implements = %v", node.Implements)
	}
	shape := typeByName(a, "Shape")
	if shape == nil || shape.Kind != "trait" {
		t.Fatalf("Shape = %+v", shape)
	}
	if len(shape.Methods) != 2 {
		t.Fatalf("Shape methods = %+v", shape.Methods)
	}
	if fnByKey(a, "area", "Node") == nil {
		t.Fatalf("trait impl method missing: %+v", a.Functions)
	}
	newFn := fnByKey(a, "new", "Node")
	if newFn == nil || newFn.Kind != "method" {
		t.Fatalf("new method = %+v", newFn)
	}
	count := fnByKey(a, "count", "Node")
	if count == nil || count.BigO != "O(n)" {
		t.Fatalf("count = %+v", count)
	}
	fib := fnByKey(a, "fib", "")
	if fib == nil || fib.BigO != "O(2^n)" || !fib.Recursive {
		t.Fatalf("fib = %+v", fib)
	}
	sortAll := fnByKey(a, "sort_all", "")
	if sortAll == nil || sortAll.BigO != "O(n²)" {
		t.Fatalf("sort_all = %+v", sortAll)
	}
	if !hasEvidence(sortAll, "sort call") {
		t.Fatalf("sort_all evidence = %v", sortAll.Evidence)
	}
}

func TestGenericCppStructure(t *testing.T) {
	src := `#include <vector>
#include <algorithm>

template <class T>
struct Box { T value; };

struct Point {
    int x;
    int y;
    int sum() const;
    Point(int x, int y) : x(x), y(y) {}
    ~Point() {}
};

int Point::sum() const { return x + y; }

void bubble(std::vector<int>& v) {
    for (size_t i = 0; i < v.size(); ++i) {
        for (size_t j = 0; j < v.size() - i - 1; ++j) {
            std::swap(v[j], v[j + 1]);
        }
    }
}

int fib(int n) {
    return n < 2 ? n : fib(n - 1) + fib(n - 2);
}

template <typename T>
T maxOf(T a, T b) {
    return a > b ? a : b;
}

class Widget : public Base, private Mixin {
public:
    int size;
    void draw();
};
`
	a := mustAnalyze(t, "sample.cpp", "cpp", src)
	if got := strings.Join(a.Imports, ","); got != "algorithm,vector" {
		t.Fatalf("imports = %q", got)
	}
	point := typeByName(a, "Point")
	if point == nil || point.Kind != "struct" {
		t.Fatalf("Point = %+v", point)
	}
	box := typeByName(a, "Box")
	if box == nil || box.Kind != "struct" || len(box.Fields) != 1 || box.Fields[0].Name != "value" {
		t.Fatalf("Box = %+v", box)
	}
	if typeByName(a, "T") != nil {
		t.Fatalf("template parameter parsed as type: %+v", a.Types)
	}
	if len(point.Fields) != 2 || point.Fields[0].Name != "x" || point.Fields[1].Name != "y" {
		t.Fatalf("Point fields = %+v", point.Fields)
	}
	sum := fnByKey(a, "sum", "Point")
	if sum == nil || sum.Kind != "method" {
		t.Fatalf("sum = %+v", sum)
	}
	bubble := fnByKey(a, "bubble", "")
	if bubble == nil || bubble.BigO != "O(n²)" {
		t.Fatalf("bubble = %+v", bubble)
	}
	fib := fnByKey(a, "fib", "")
	if fib == nil || fib.BigO != "O(2^n)" || !fib.Recursive {
		t.Fatalf("fib = %+v", fib)
	}
	if dtor := fnByKey(a, "~Point", "Point"); dtor == nil {
		t.Fatalf("destructor missing: %+v", a.Functions)
	}
	if maxOf := fnByKey(a, "maxOf", ""); maxOf == nil || !strings.HasPrefix(maxOf.Signature, "template") {
		t.Fatalf("template function = %+v", maxOf)
	}
	widget := typeByName(a, "Widget")
	if widget == nil || widget.Kind != "class" {
		t.Fatalf("Widget = %+v", widget)
	}
	if got := strings.Join(widget.Bases, ","); got != "Base,Mixin" {
		t.Fatalf("Widget bases = %q", got)
	}
	if len(widget.Fields) != 1 || widget.Fields[0].Name != "size" {
		t.Fatalf("Widget fields = %+v", widget.Fields)
	}
	if len(widget.Methods) != 1 || widget.Methods[0].Name != "draw" {
		t.Fatalf("Widget methods = %+v", widget.Methods)
	}
}

func TestGenericCStructure(t *testing.T) {
	src := `#include <stdlib.h>

typedef struct {
    int x;
} Point;

int compare(const void *a, const void *b) {
    return *(const int *)a - *(const int *)b;
}

void sort_only(int *a, int n) {
    qsort(a, n, sizeof(int), compare);
}

void sort_and_scan(int *a, int n) {
    qsort(a, n, sizeof(int), compare);
    for (int i = 0; i < n; i++) {
        for (int j = 0; j < n; j++) {
            a[i] ^= a[j];
        }
    }
}
`
	a := mustAnalyze(t, "sort.c", "c", src)
	if got := strings.Join(a.Imports, ","); got != "stdlib.h" {
		t.Fatalf("imports = %q", got)
	}
	if len(a.Functions) != 3 {
		t.Fatalf("functions = %+v", a.Functions)
	}
	point := typeByName(a, "Point")
	if point == nil || point.Kind != "struct" || len(point.Fields) != 1 || point.Fields[0].Name != "x" {
		t.Fatalf("typedef Point = %+v", point)
	}
	sortOnly := fnByKey(a, "sort_only", "")
	if sortOnly == nil || sortOnly.BigO != "O(n log n)" {
		t.Fatalf("sort_only = %+v", sortOnly)
	}
	if !hasEvidence(sortOnly, "sort call") {
		t.Fatalf("sort_only evidence = %v", sortOnly.Evidence)
	}
	scan := fnByKey(a, "sort_and_scan", "")
	if scan == nil || scan.BigO != "O(n²)" {
		t.Fatalf("sort_and_scan = %+v", scan)
	}
}

func TestGenericJavaStructure(t *testing.T) {
	src := `package demo;

import java.util.List;

public class Counter implements Runnable {
    private int count;
    private int x, y;

    public Counter(int start) { this.count = start; }

    public void run() {
        for (int i = 0; i < count; i++) {
            count += i;
        }
    }

    public static int find(int[] a, int target) {
        int lo = 0, hi = a.length;
        while (lo < hi) {
            int mid = (lo + hi) / 2;
            if (a[mid] == target) return mid;
            if (a[mid] < target) lo = mid + 1; else hi = mid;
        }
        return -1;
    }
}

record Pair(int left, int right) {}
enum Color {
    RED(1), GREEN(2);
    private final int code;
    Color(int code) { this.code = code; }
    int code() { return code; }
}
`
	a := mustAnalyze(t, "Counter.java", "java", src)
	if got := strings.Join(a.Imports, ","); got != "java.util.List" {
		t.Fatalf("imports = %q", got)
	}
	counter := typeByName(a, "Counter")
	if counter == nil || !counter.Exported {
		t.Fatalf("Counter = %+v", counter)
	}
	if len(counter.Implements) != 1 || counter.Implements[0] != "Runnable" {
		t.Fatalf("implements = %v", counter.Implements)
	}
	if len(counter.Fields) != 3 || counter.Fields[0].Name != "count" ||
		counter.Fields[1].Name != "x" || counter.Fields[2].Name != "y" {
		t.Fatalf("fields = %+v", counter.Fields)
	}
	if counter.Fields[2].Type != "int" {
		t.Fatalf("carried type = %q", counter.Fields[2].Type)
	}
	if fnByKey(a, "Counter", "Counter") == nil {
		t.Fatalf("constructor missing: %+v", a.Functions)
	}
	run := fnByKey(a, "run", "Counter")
	if run == nil || run.BigO != "O(n)" {
		t.Fatalf("run = %+v", run)
	}
	find := fnByKey(a, "find", "Counter")
	if find == nil || find.BigO != "O(log n)" {
		t.Fatalf("find = %+v", find)
	}
	pair := typeByName(a, "Pair")
	if pair == nil || len(pair.Fields) != 2 || pair.Fields[0].Name != "left" {
		t.Fatalf("record Pair = %+v", pair)
	}
	color := typeByName(a, "Color")
	if color == nil || color.Kind != "enum" {
		t.Fatalf("Color = %+v", color)
	}
	names := make([]string, 0, len(color.Fields))
	for _, f := range color.Fields {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "RED,GREEN,code" {
		t.Fatalf("Color fields = %+v (methods %+v)", color.Fields, color.Methods)
	}
	if fnByKey(a, "Color", "Color") == nil || fnByKey(a, "code", "Color") == nil {
		t.Fatalf("enum members missing: %+v", a.Functions)
	}
}

func TestGenericHTMLOutline(t *testing.T) {
	src := `<!DOCTYPE html>
<html>
<head><title>Hi</title></head>
<body>
  <div id="app" class="root wide">
    <ul>
      <li>One</li>
      <li>Two</li>
    </ul>
    <img src="x.png" />
  </div>
</body>
</html>
`
	a, err := analyzeHTML("index.html", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		tag     string
		depth   int
		id      string
		text    string
		classes string
	}{
		{"html", 0, "", "", ""},
		{"head", 1, "", "", ""},
		{"title", 2, "", "Hi", ""},
		{"body", 1, "", "", ""},
		{"div", 2, "app", "", "root wide"},
		{"ul", 3, "", "", ""},
		{"li", 4, "", "One", ""},
		{"li", 4, "", "Two", ""},
		{"img", 3, "", "", ""},
	}
	if len(a.HTML) != len(want) {
		t.Fatalf("html elements = %d: %+v", len(a.HTML), a.HTML)
	}
	for i, w := range want {
		el := a.HTML[i]
		if el.Tag != w.tag || el.Depth != w.depth || el.ID != w.id || el.Text != w.text {
			t.Fatalf("element %d = %+v, want %+v", i, el, w)
		}
		if w.classes != "" && strings.Join(el.Classes, " ") != w.classes {
			t.Fatalf("element %d classes = %v", i, el.Classes)
		}
	}
	if a.Metrics.DominantBigO != "—" {
		t.Fatalf("dominant big-o = %q", a.Metrics.DominantBigO)
	}
}

func TestGenericCSSRules(t *testing.T) {
	src := `/* comment */
:root { --x: 1; }
.card, .panel {
  color: red;
  background: url("a;b.png");
}
@media (max-width: 600px) {
  .card { display: none; }
}
.button {
  &:hover { color: blue; }
  padding: 0;
}
[data-x="a b"] { color: green; }
`
	a, err := analyzeCSS("style.scss", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Rules) != 6 {
		t.Fatalf("rules = %d: %+v", len(a.Rules), a.Rules)
	}
	selectors := make([]string, 0, len(a.Rules))
	for _, r := range a.Rules {
		selectors = append(selectors, r.Selector)
	}
	if got := strings.Join(selectors, "|"); got != `:root|.card, .panel|.card|.button|&:hover|[data-x="a b"]` {
		t.Fatalf("selectors = %q", got)
	}
	panel := a.Rules[1]
	if len(panel.Declarations) != 2 {
		t.Fatalf("panel declarations = %+v", panel.Declarations)
	}
	if !strings.Contains(panel.Declarations[1], `url("a;b.png")`) {
		t.Fatalf("string declaration mangled: %q", panel.Declarations[1])
	}
	button := a.Rules[3]
	if len(button.Declarations) != 1 || button.Declarations[0] != "padding: 0" {
		t.Fatalf("button declarations = %+v", button.Declarations)
	}
}

func TestGenericJSAnonymousCallbacks(t *testing.T) {
	src := `function helper(x) { return x; }

function render(items) {
  items.forEach(x => { helper(x); });
  for (const it of items) {
    helper(it);
  }
  return items;
}
`
	a := mustAnalyze(t, "callbacks.js", "javascript", src)
	if len(a.Functions) != 2 {
		t.Fatalf("functions = %+v", a.Functions)
	}
	if fnByKey(a, "render", "") == nil {
		t.Fatalf("render missing: %+v", a.Functions)
	}
	for _, f := range a.Functions {
		if f.Name == "forEach" || f.Name == "x" {
			t.Fatalf("anonymous callback leaked: %+v", f)
		}
	}
}

func TestGenericTSX(t *testing.T) {
	src := `export function App({ items }: Props) {
  return (
    <div className="app">
      {items.map(item => <span key={item}>{item}</span>)}
    </div>
  );
}
`
	a := mustAnalyze(t, "app.tsx", "typescriptreact", src)
	if len(a.Functions) != 1 || a.Functions[0].Name != "App" {
		t.Fatalf("functions = %+v", a.Functions)
	}
	if a.Functions[0].Kind != "function" {
		t.Fatalf("App kind = %q", a.Functions[0].Kind)
	}
	for _, ty := range a.Types {
		if ty.Name == "Props" {
			t.Fatalf("JSX tag parsed as type: %+v", ty)
		}
	}
	if a.Functions[0].EndLine != 7 {
		t.Fatalf("App end line = %d", a.Functions[0].EndLine)
	}
}

func TestGenericHTMLRawText(t *testing.T) {
	src := `<div><script>if (a < b) { x(); }</script><p>ok</p></div>`
	a, err := analyzeHTML("raw.html", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	tags := make([]string, 0, len(a.HTML))
	for _, el := range a.HTML {
		tags = append(tags, el.Tag)
	}
	if got := strings.Join(tags, ","); got != "div,script,p" {
		t.Fatalf("tags = %q", got)
	}
	if a.HTML[2].Depth != 1 || a.HTML[2].Text != "ok" {
		t.Fatalf("p element = %+v", a.HTML[2])
	}
}

func TestGenericMalformedInput(t *testing.T) {
	cases := []struct {
		lang string
		src  string
	}{
		{"python", "def broken(:\n    x = 'unterminated\n  y = 1\n"},
		{"javascript", "function f( { const x = `unterminated\nclass {\n"},
		{"typescript", "const f = (x: => {\ninterface {\n"},
		{"rust", "fn f() { let s = r#\"unterminated\nstruct {\n"},
		{"c", "int f() { /* unterminated\n char c = '\n"},
		{"cpp", "class : public { void f( { } } }\n"},
		{"java", "class { void f() { if (x { y(); }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			a, err := analyzeGeneric("broken", tc.lang, []byte(tc.src))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.Lines < 0 {
				t.Fatalf("bad line count: %d", a.Lines)
			}
			for _, f := range a.Functions {
				if f.EndLine < f.StartLine || f.EndChar < 0 {
					t.Fatalf("invalid function %+v", f)
				}
			}
			for _, ty := range a.Types {
				if ty.EndLine < ty.StartLine {
					t.Fatalf("invalid type %+v", ty)
				}
			}
		})
	}
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"html", "<div><span class=\"x>text</div><<>>"},
		{"css", ".a { color: red; /* unterminated\n} @media {"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "html" {
				if _, err := analyzeHTML("broken.html", []byte(tc.src)); err != nil {
					t.Fatal(err)
				}
			} else if _, err := analyzeCSS("broken.css", []byte(tc.src)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGenericDeterministic(t *testing.T) {
	src := `import x from "x";
export class A extends B {
  f(n: number) {
    for (let i = 0; i < n; i++) { this.g(i); }
    return n;
  }
  g(v: number) { return v; }
}
`
	a1 := mustAnalyze(t, "a.ts", "typescript", src)
	a2 := mustAnalyze(t, "a.ts", "typescript", src)
	if !reflect.DeepEqual(a1, a2) {
		t.Fatalf("analysis is not deterministic:\n%+v\n%+v", a1, a2)
	}
}

func TestGenericUnsupportedLanguage(t *testing.T) {
	a, err := analyzeGeneric("x.unknown", "cobol", []byte("MOVE 1 TO X."))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Notes) == 0 || !strings.Contains(a.Notes[0], "unsupported") {
		t.Fatalf("notes = %v", a.Notes)
	}
	if a.Metrics.DominantBigO != "—" {
		t.Fatalf("dominant = %q", a.Metrics.DominantBigO)
	}
}
