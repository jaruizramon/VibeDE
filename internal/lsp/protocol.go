package lsp

import "encoding/json"

// This file defines the subset of LSP 3.17 that VibeDE uses. All positions are
// zero-based and use UTF-16 code units, as required by the protocol.

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type LocationLink struct {
	OriginSelectionRange *Range `json:"originSelectionRange,omitempty"`
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

// NavLocation is the normalized navigation result returned by the API.
type NavLocation struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine"`
	StartChar int    `json:"startChar"`
	EndLine   int    `json:"endLine"`
	EndChar   int    `json:"endChar"`
}

type Diagnostic struct {
	Range    Range     `json:"range"`
	Severity int       `json:"severity,omitempty"` // 1 error, 2 warning, 3 info, 4 hint
	Code     any       `json:"code,omitempty"`
	Source   string    `json:"source,omitempty"`
	Message  string    `json:"message"`
	Tags     []int     `json:"tags,omitempty"`
	Related  []Related `json:"relatedInformation,omitempty"`
}

type Related struct {
	Location Location `json:"location"`
	Message  string   `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// FileDiagnostics is the API representation of diagnostics for one file.
type FileDiagnostics struct {
	Path        string       `json:"path"`
	Server      string       `json:"server"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Counts      DiagCounts   `json:"counts"`
}

type DiagCounts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Info     int `json:"info"`
	Hints    int `json:"hints"`
}

func (c *DiagCounts) add(sev int) {
	switch sev {
	case 1:
		c.Errors++
	case 2:
		c.Warnings++
	case 3:
		c.Info++
	case 4:
		c.Hints++
	}
}

type MarkupContent struct {
	Kind  string `json:"kind"` // markdown | plaintext
	Value string `json:"value"`
}

type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// rawHover accepts MarkedString, MarkedString[] or MarkupContent.
type rawHover struct {
	Contents json.RawMessage `json:"contents"`
	Range    *Range          `json:"range"`
}

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

type SymbolInformation struct {
	Name     string   `json:"name"`
	Kind     int      `json:"kind"`
	Location Location `json:"location"`
}

// Symbol is the normalized outline entry used by the UI.
type Symbol struct {
	Name      string   `json:"name"`
	Detail    string   `json:"detail,omitempty"`
	Kind      string   `json:"kind"`
	StartLine int      `json:"startLine"`
	StartChar int      `json:"startChar"`
	EndLine   int      `json:"endLine"`
	EndChar   int      `json:"endChar"`
	Children  []Symbol `json:"children,omitempty"`
}

// SymbolKind names follow the LSP specification.
var SymbolKind = map[int]string{
	1: "File", 2: "Module", 3: "Namespace", 4: "Package", 5: "Class",
	6: "Method", 7: "Property", 8: "Field", 9: "Constructor", 10: "Enum",
	11: "Interface", 12: "Function", 13: "Variable", 14: "Constant",
	15: "String", 16: "Number", 17: "Boolean", 18: "Array", 19: "Object",
	20: "Key", 21: "Null", 22: "EnumMember", 23: "Struct", 24: "Event",
	25: "Operator", 26: "TypeParameter",
}

type InitializeResult struct {
	Capabilities struct {
		DefinitionProvider     any `json:"definitionProvider,omitempty"`
		ReferencesProvider     any `json:"referencesProvider,omitempty"`
		HoverProvider          any `json:"hoverProvider,omitempty"`
		DocumentSymbolProvider any `json:"documentSymbolProvider,omitempty"`
		ImplementationProvider any `json:"implementationProvider,omitempty"`
		TypeDefinitionProvider any `json:"typeDefinitionProvider,omitempty"`
	} `json:"capabilities"`
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type didOpenParams struct {
	TextDocument struct {
		URI        string `json:"uri"`
		LanguageID string `json:"languageId"`
		Version    int    `json:"version"`
		Text       string `json:"text"`
	} `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type TextDocumentPosition struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position Position `json:"position"`
}

type referencesParams struct {
	TextDocumentPosition
	Context struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

type documentSymbolsParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

// Protocol request/notification methods used by this package.
const (
	mInitialize         = "initialize"
	mInitialized        = "initialized"
	mShutdown           = "shutdown"
	mExit               = "exit"
	mDidOpen            = "textDocument/didOpen"
	mDidChange          = "textDocument/didChange"
	mDidClose           = "textDocument/didClose"
	mDidChangeConfig    = "workspace/didChangeConfiguration"
	mDefinition         = "textDocument/definition"
	mReferences         = "textDocument/references"
	mHover              = "textDocument/hover"
	mDocumentSymbol     = "textDocument/documentSymbol"
	mImplementation     = "textDocument/implementation"
	mTypeDefinition     = "textDocument/typeDefinition"
	mPublishDiagnostics = "textDocument/publishDiagnostics"
)
