//go:build js && wasm

package main

import "time"

type Info struct {
	Name       string    `json:"name"`
	Version    string    `json:"version"`
	Root       string    `json:"root"`
	ReadOnly   bool      `json:"readOnly"`
	Detachable bool      `json:"detachable"`
	StartedAt  time.Time `json:"startedAt"`
	Languages  []string  `json:"languages"`
	Limits     Limits    `json:"limits"`
	Servers    []Server  `json:"servers"`
	Watcher    Watcher   `json:"watcher"`
	Features   []string  `json:"features"`
	History    int       `json:"history"`
}

type Limits struct {
	MaxFileBytes     int64 `json:"maxFileBytes"`
	MaxSnapshotBytes int64 `json:"maxSnapshotBytes"`
	MaxDiagramNodes  int   `json:"maxDiagramNodes"`
	MaxRenderedLines int   `json:"maxRenderedLines"`
}

type Watcher struct {
	Seeded        bool  `json:"seeded"`
	Snapshots     int   `json:"snapshots"`
	SnapshotBytes int64 `json:"snapshotBytes"`
}

type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	IsDir    bool      `json:"isDir"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified,omitempty"`
	Language string    `json:"language,omitempty"`
	Symlink  bool      `json:"symlink,omitempty"`
}

type TreeResponse struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
}

type FileResponse struct {
	Path     string    `json:"path"`
	Language string    `json:"language"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Lines    int       `json:"lines"`
	Content  string    `json:"content"`
}

type Analysis struct {
	Path      string        `json:"path"`
	Language  string        `json:"language"`
	Bytes     int64         `json:"bytes"`
	Lines     int           `json:"lines"`
	SLOC      int           `json:"sloc"`
	Functions []Function    `json:"functions"`
	Types     []Type        `json:"types"`
	Imports   []string      `json:"imports,omitempty"`
	Metrics   Metrics       `json:"metrics"`
	HTML      []HTMLElement `json:"html,omitempty"`
	Rules     []CSSRule     `json:"rules,omitempty"`
	Notes     []string      `json:"notes,omitempty"`
}

type Function struct {
	Name       string   `json:"name"`
	Receiver   string   `json:"receiver,omitempty"`
	Kind       string   `json:"kind"`
	Signature  string   `json:"signature"`
	StartLine  int      `json:"startLine"`
	EndLine    int      `json:"endLine"`
	StartChar  int      `json:"startChar"`
	EndChar    int      `json:"endChar"`
	Complexity int      `json:"complexity"`
	BigO       string   `json:"bigO"`
	Evidence   []string `json:"evidence,omitempty"`
	Calls      []string `json:"calls,omitempty"`
	Recursive  bool     `json:"recursive,omitempty"`
	Exported   bool     `json:"exported,omitempty"`
}

type Type struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	StartLine  int      `json:"startLine"`
	EndLine    int      `json:"endLine"`
	Fields     []Field  `json:"fields,omitempty"`
	Methods    []Method `json:"methods,omitempty"`
	Implements []string `json:"implements,omitempty"`
	Embedded   []string `json:"embedded,omitempty"`
	Bases      []string `json:"bases,omitempty"`
	Exported   bool     `json:"exported,omitempty"`
}

type Field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Tag      string `json:"tag,omitempty"`
	Embedded bool   `json:"embedded,omitempty"`
}

type Method struct {
	Name      string `json:"name"`
	Signature string `json:"signature,omitempty"`
}

type HTMLElement struct {
	Tag     string   `json:"tag"`
	ID      string   `json:"id,omitempty"`
	Classes []string `json:"classes,omitempty"`
	Line    int      `json:"line"`
	Depth   int      `json:"depth"`
	Text    string   `json:"text,omitempty"`
}

type CSSRule struct {
	Selector     string   `json:"selector"`
	Line         int      `json:"line"`
	Declarations []string `json:"declarations,omitempty"`
}

type Metrics struct {
	Functions       int    `json:"functions"`
	MaxComplexity   int    `json:"maxComplexity"`
	AvgComplexity   int    `json:"avgComplexity"`
	TotalComplexity int    `json:"totalComplexity"`
	Types           int    `json:"types"`
	DominantBigO    string `json:"dominantBigO"`
}

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Code     any    `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

type DiagCounts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Info     int `json:"info"`
	Hints    int `json:"hints"`
}

type FileDiagnostics struct {
	Path        string       `json:"path"`
	Server      string       `json:"server"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Counts      DiagCounts   `json:"counts"`
}

type Server struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Languages   []string `json:"languages"`
	Installed   bool     `json:"installed"`
	Status      string   `json:"status"`
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	Install     string   `json:"install,omitempty"`
	Docs        string   `json:"docs,omitempty"`
	Error       string   `json:"error,omitempty"`
	PID         int      `json:"pid,omitempty"`
	OpenDocs    int      `json:"openDocs"`
	Diagnostics int      `json:"diagnostics"`
	Logs        []string `json:"logs,omitempty"`
}

type DiffStats struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

type DiffLine struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	OldNo int    `json:"oldNo,omitempty"`
	NewNo int    `json:"newNo,omitempty"`
}

type Hunk struct {
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Lines    []DiffLine `json:"lines"`
}

type Change struct {
	Path         string    `json:"path"`
	Kind         string    `json:"kind"`
	IsDir        bool      `json:"isDir,omitempty"`
	Language     string    `json:"language,omitempty"`
	Size         int64     `json:"size,omitempty"`
	Binary       bool      `json:"binary,omitempty"`
	At           time.Time `json:"at"`
	Stats        DiffStats `json:"stats"`
	Hunks        []Hunk    `json:"hunks,omitempty"`
	HunksOmitted bool      `json:"hunksOmitted,omitempty"`
}

type NavLocation struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine"`
	StartChar int    `json:"startChar"`
	EndLine   int    `json:"endLine"`
	EndChar   int    `json:"endChar"`
}

type CodeMatch struct {
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	MatchLen    int    `json:"matchLen"`
	Occurrences int    `json:"occurrences"`
	Preview     string `json:"preview"`
	MatchStart  int    `json:"matchStart"`
}

type CodeSearchResponse struct {
	Query         string      `json:"query"`
	CaseSensitive bool        `json:"caseSensitive"`
	Matches       []CodeMatch `json:"matches"`
	Files         int         `json:"files"`
	Scanned       int         `json:"scanned"`
	Truncated     bool        `json:"truncated"`
	TookMS        int64       `json:"tookMs"`
}

type DiagramDoc struct {
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	SVG     string `json:"svg"`
	DOT     string `json:"dot"`
	Mermaid string `json:"mermaid"`
	Nodes   int    `json:"nodes"`
	Edges   int    `json:"edges"`
}

type APIError struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Install string `json:"install,omitempty"`
	Docs    string `json:"docs,omitempty"`
}
