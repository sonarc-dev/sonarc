// Package syntax highlights source code.
//
// Highlighting is done by a hand-written scanner driven by a per-language
// description, not by regular expressions. Two reasons: a scanner handles the
// state that carries across lines — block comments, raw strings — directly and
// correctly, and it is fast enough to run on every visible line of every frame
// without the editor feeling sluggish over ssh.
package syntax

import (
	"path/filepath"
	"strings"
)

// Class is what a token should be colored as.
type Class uint8

const (
	ClassNone Class = iota
	ClassKeyword
	ClassType
	ClassString
	ClassComment
	ClassNumber
	ClassFunc
	ClassPreproc
)

// Token is a colored span of one line, in byte offsets.
type Token struct {
	Start, End int
	Class      Class
}

// Language describes how to scan one language.
type Language struct {
	Name  string
	Exts  []string
	Files []string // exact file names, for things like Makefile

	LineComment  string
	BlockStart   string
	BlockEnd     string
	NestedBlocks bool // Rust allows /* /* */ */

	// Quotes lists the characters that open a single-line string.
	Quotes string
	// RawQuote opens a string that spans lines with no escapes, such as Go's
	// backtick. Zero when the language has none.
	RawQuote byte
	// TripleQuote enables Python's ''' and """ multi-line strings.
	TripleQuote bool
	// Preproc treats a leading # as a preprocessor directive, as in C.
	Preproc bool

	Keywords map[string]bool
	Types    map[string]bool
}

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// Languages are the built-in definitions. C, Go and Python come first because
// they are what this editor is aimed at; the rest share the same scanner.
var Languages = []*Language{
	{
		Name:        "c",
		Exts:        []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx", ".inl"},
		LineComment: "//", BlockStart: "/*", BlockEnd: "*/",
		Quotes: "\"'", Preproc: true,
		Keywords: set("auto", "break", "case", "const", "continue", "default", "do",
			"else", "enum", "extern", "for", "goto", "if", "inline", "register",
			"restrict", "return", "sizeof", "static", "struct", "switch", "typedef",
			"union", "volatile", "while", "_Atomic", "_Static_assert", "alignof",
			"asm", "class", "namespace", "template", "typename", "public", "private",
			"protected", "virtual", "override", "new", "delete", "this", "using",
			"try", "catch", "throw", "constexpr", "nullptr", "explicit", "friend",
			"operator", "noexcept", "decltype", "static_cast", "dynamic_cast",
			"const_cast", "reinterpret_cast"),
		Types: set("void", "char", "short", "int", "long", "float", "double",
			"signed", "unsigned", "bool", "size_t", "ssize_t", "ptrdiff_t",
			"int8_t", "int16_t", "int32_t", "int64_t", "uint8_t", "uint16_t",
			"uint32_t", "uint64_t", "intptr_t", "uintptr_t", "FILE", "va_list",
			"wchar_t", "char16_t", "char32_t", "true", "false", "NULL"),
	},
	{
		Name:        "go",
		Exts:        []string{".go"},
		LineComment: "//", BlockStart: "/*", BlockEnd: "*/",
		Quotes: "\"'", RawQuote: '`',
		Keywords: set("break", "case", "chan", "const", "continue", "default",
			"defer", "else", "fallthrough", "for", "func", "go", "goto", "if",
			"import", "interface", "map", "package", "range", "return", "select",
			"struct", "switch", "type", "var"),
		Types: set("bool", "byte", "complex64", "complex128", "error", "float32",
			"float64", "int", "int8", "int16", "int32", "int64", "rune", "string",
			"uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "any",
			"true", "false", "nil", "iota", "make", "new", "len", "cap", "append",
			"copy", "delete", "panic", "recover", "print", "println", "close"),
	},
	{
		Name:        "python",
		Exts:        []string{".py", ".pyi", ".pyw"},
		LineComment: "#",
		Quotes:      "\"'", TripleQuote: true,
		Keywords: set("and", "as", "assert", "async", "await", "break", "class",
			"continue", "def", "del", "elif", "else", "except", "finally", "for",
			"from", "global", "if", "import", "in", "is", "lambda", "nonlocal",
			"not", "or", "pass", "raise", "return", "try", "while", "with", "yield",
			"match", "case"),
		Types: set("bool", "bytes", "complex", "dict", "float", "frozenset", "int",
			"list", "object", "set", "str", "tuple", "type", "None", "True", "False",
			"self", "cls", "len", "range", "print", "open", "isinstance", "super",
			"enumerate", "zip", "map", "filter", "sorted", "sum", "min", "max"),
	},
	{
		Name:        "rust",
		Exts:        []string{".rs"},
		LineComment: "//", BlockStart: "/*", BlockEnd: "*/", NestedBlocks: true,
		Quotes: "\"'",
		Keywords: set("as", "async", "await", "break", "const", "continue", "crate",
			"dyn", "else", "enum", "extern", "fn", "for", "if", "impl", "in", "let",
			"loop", "match", "mod", "move", "mut", "pub", "ref", "return", "self",
			"static", "struct", "super", "trait", "type", "unsafe", "use", "where",
			"while"),
		Types: set("bool", "char", "f32", "f64", "i8", "i16", "i32", "i64", "i128",
			"isize", "str", "u8", "u16", "u32", "u64", "u128", "usize", "String",
			"Vec", "Option", "Result", "Box", "true", "false", "None", "Some", "Ok",
			"Err"),
	},
	{
		Name:        "javascript",
		Exts:        []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"},
		LineComment: "//", BlockStart: "/*", BlockEnd: "*/",
		Quotes: "\"'", RawQuote: '`',
		Keywords: set("async", "await", "break", "case", "catch", "class", "const",
			"continue", "debugger", "default", "delete", "do", "else", "export",
			"extends", "finally", "for", "function", "if", "import", "in",
			"instanceof", "let", "new", "of", "return", "static", "super", "switch",
			"this", "throw", "try", "typeof", "var", "void", "while", "with", "yield",
			"interface", "type", "enum", "implements", "declare", "namespace",
			"public", "private", "protected", "readonly", "as", "satisfies"),
		Types: set("any", "boolean", "never", "number", "object", "string", "symbol",
			"undefined", "unknown", "bigint", "true", "false", "null", "Array",
			"Promise", "Map", "Set", "Object", "String", "Number", "Boolean"),
	},
	{
		Name:        "java",
		Exts:        []string{".java"},
		LineComment: "//", BlockStart: "/*", BlockEnd: "*/",
		Quotes: "\"'",
		Keywords: set("abstract", "assert", "break", "case", "catch", "class",
			"continue", "default", "do", "else", "enum", "extends", "final",
			"finally", "for", "if", "implements", "import", "instanceof",
			"interface", "native", "new", "package", "private", "protected",
			"public", "return", "static", "strictfp", "super", "switch",
			"synchronized", "this", "throw", "throws", "transient", "try", "var",
			"volatile", "while", "record", "sealed", "yield"),
		Types: set("boolean", "byte", "char", "double", "float", "int", "long",
			"short", "void", "String", "Object", "Integer", "Boolean", "Double",
			"List", "Map", "Set", "true", "false", "null"),
	},
	{
		Name:        "shell",
		Exts:        []string{".sh", ".bash", ".zsh", ".ksh"},
		Files:       []string{".bashrc", ".bash_profile", ".zshrc", ".profile"},
		LineComment: "#",
		Quotes:      "\"'",
		Keywords: set("if", "then", "else", "elif", "fi", "case", "esac", "for",
			"while", "until", "do", "done", "in", "function", "select", "time",
			"return", "break", "continue", "local", "export", "readonly", "declare",
			"typeset", "unset", "shift", "source", "alias", "trap", "set"),
		Types: set("echo", "printf", "read", "cd", "pwd", "test", "exit", "eval",
			"exec", "true", "false"),
	},
	{
		Name:        "make",
		Exts:        []string{".mk", ".make"},
		Files:       []string{"Makefile", "makefile", "GNUmakefile"},
		LineComment: "#",
		Quotes:      "\"'",
		Keywords: set("ifeq", "ifneq", "ifdef", "ifndef", "else", "endif", "include",
			"define", "endef", "export", "unexport", "override", "vpath", ".PHONY"),
		Types: set("shell", "wildcard", "patsubst", "subst", "foreach", "filter",
			"filter-out", "notdir", "dir", "basename", "addprefix", "addsuffix",
			"call", "eval", "value", "origin", "error", "warning", "info"),
	},
	{
		Name:     "json",
		Exts:     []string{".json"},
		Quotes:   "\"",
		Keywords: set("true", "false", "null"),
		Types:    set(),
	},
	{
		Name:        "yaml",
		Exts:        []string{".yaml", ".yml"},
		LineComment: "#",
		Quotes:      "\"'",
		Keywords:    set("true", "false", "null", "yes", "no", "on", "off", "~"),
		Types:       set(),
	},
	{
		Name:        "toml",
		Exts:        []string{".toml"},
		LineComment: "#",
		Quotes:      "\"'",
		Keywords:    set("true", "false"),
		Types:       set(),
	},
}

// byExt and byFile index the languages for lookup.
var (
	byExt  = map[string]*Language{}
	byFile = map[string]*Language{}
	byName = map[string]*Language{}
)

func init() {
	for _, l := range Languages {
		byName[l.Name] = l
		for _, e := range l.Exts {
			byExt[e] = l
		}
		for _, f := range l.Files {
			byFile[f] = l
		}
	}
}

// Detect picks a language for a path, returning nil when none matches.
func Detect(path string) *Language {
	if path == "" {
		return nil
	}
	base := filepath.Base(path)
	if l, ok := byFile[base]; ok {
		return l
	}
	if l, ok := byExt[strings.ToLower(filepath.Ext(base))]; ok {
		return l
	}
	return nil
}

// ByName returns a language by its name, for an explicit override.
func ByName(name string) *Language { return byName[strings.ToLower(name)] }
