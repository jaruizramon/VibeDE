package workspace

import (
	"path"
	"strings"
)

// Language maps a file path to a display language name.
func Language(rel string) string {
	ext := strings.ToLower(path.Ext(rel))
	switch ext {
	case ".go":
		return "go"
	case ".py", ".pyi", ".pyw":
		return "python"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".html", ".htm", ".xhtml":
		return "html"
	case ".css":
		return "css"
	case ".scss", ".sass":
		return "scss"
	case ".less":
		return "less"
	case ".json":
		return "json"
	case ".jsonc":
		return "jsonc"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".c++", ".hh", ".hpp", ".hxx", ".ino":
		return "cpp"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".cs":
		return "csharp"
	case ".swift":
		return "swift"
	case ".lua":
		return "lua"
	case ".sh", ".bash", ".zsh", ".fish":
		return "shell"
	case ".sql":
		return "sql"
	case ".md", ".markdown", ".mdx":
		return "markdown"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".xml", ".svg":
		return "xml"
	case ".proto":
		return "proto"
	case ".vue":
		return "vue"
	case ".svelte":
		return "svelte"
	case ".dart":
		return "dart"
	default:
		return ""
	}
}

// ServerGroup returns the language-server registry key for a language. Several
// file languages share one server (js/ts/jsx/tsx -> typescript, c/cpp -> c).
func ServerGroup(lang string) string {
	switch lang {
	case "go":
		return "go"
	case "python":
		return "python"
	case "javascript", "javascriptreact", "typescript", "typescriptreact":
		return "typescript"
	case "html":
		return "html"
	case "css", "scss", "less":
		return "css"
	case "json", "jsonc":
		return "json"
	case "yaml":
		return "yaml"
	case "markdown":
		return "markdown"
	case "c", "cpp":
		return "c"
	case "rust":
		return "rust"
	case "java":
		return "java"
	case "lua":
		return "lua"
	case "shell":
		return "shell"
	default:
		return ""
	}
}
