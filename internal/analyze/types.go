// Package analyze provides language-aware structure, complexity and Big-O
// analysis. Go analysis uses go/ast; other languages use a deterministic
// token/indentation analyzer. Results are static estimates, not runtime
// measurements.
package analyze

// Analysis is the complete static view of one file.
type Analysis struct {
	Path      string        `json:"path"`
	Language  string        `json:"language"`
	Bytes     int64         `json:"bytes"`
	Lines     int           `json:"lines"`
	SLOC      int           `json:"sloc"`
	Functions []Function    `json:"functions"`
	Types     []TypeInfo    `json:"types"`
	Imports   []string      `json:"imports,omitempty"`
	Metrics   Metrics       `json:"metrics"`
	HTML      []HTMLElement `json:"html,omitempty"`
	Rules     []CSSRule     `json:"rules,omitempty"`
	Notes     []string      `json:"notes,omitempty"`
}

// Function describes a named function or method.
type Function struct {
	Name       string   `json:"name"`
	Receiver   string   `json:"receiver,omitempty"`
	Kind       string   `json:"kind"` // function, method, arrow, closure
	Signature  string   `json:"signature"`
	StartLine  int      `json:"startLine"` // 1-based
	EndLine    int      `json:"endLine"`
	StartChar  int      `json:"startChar"` // 0-based
	EndChar    int      `json:"endChar"`
	Complexity int      `json:"complexity"` // cyclomatic
	BigO       string   `json:"bigO"`
	Evidence   []string `json:"evidence,omitempty"`
	Calls      []string `json:"calls,omitempty"` // resolved local calls
	Recursive  bool     `json:"recursive,omitempty"`
	Exported   bool     `json:"exported,omitempty"`
}

// TypeInfo describes a struct, interface, class or type alias.
type TypeInfo struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"` // struct, interface, class, enum, type
	StartLine  int      `json:"startLine"`
	EndLine    int      `json:"endLine"`
	Fields     []Field  `json:"fields,omitempty"`
	Methods    []Method `json:"methods,omitempty"`
	Implements []string `json:"implements,omitempty"`
	Embedded   []string `json:"embedded,omitempty"`
	Bases      []string `json:"bases,omitempty"`
	Exported   bool     `json:"exported,omitempty"`
}

// Field is a struct/class field.
type Field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Tag      string `json:"tag,omitempty"`
	Embedded bool   `json:"embedded,omitempty"`
}

// Method is a method attached to a type.
type Method struct {
	Name      string `json:"name"`
	Signature string `json:"signature,omitempty"`
}

// Metrics are file-level aggregates.
type Metrics struct {
	Functions       int    `json:"functions"`
	MaxComplexity   int    `json:"maxComplexity"`
	AvgComplexity   int    `json:"avgComplexity"`
	TotalComplexity int    `json:"totalComplexity"`
	Types           int    `json:"types"`
	DominantBigO    string `json:"dominantBigO"`
}

// HTMLElement is one node in an HTML document outline.
type HTMLElement struct {
	Tag     string   `json:"tag"`
	ID      string   `json:"id,omitempty"`
	Classes []string `json:"classes,omitempty"`
	Line    int      `json:"line"`
	Depth   int      `json:"depth"`
	Text    string   `json:"text,omitempty"`
}

// CSSRule is one CSS rule.
type CSSRule struct {
	Selector     string   `json:"selector"`
	Line         int      `json:"line"`
	Declarations []string `json:"declarations,omitempty"`
}

// Big-O estimate for a whole file (used when there are no functions).
const bigONone = "—"
