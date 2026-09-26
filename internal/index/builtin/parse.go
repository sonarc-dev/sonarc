package builtin

import (
	"regexp"
	"strings"

	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// These patterns recognize definitions well enough to navigate by. They are
// heuristics, not parsers: this provider is the fallback, and a few false
// positives are a far better outcome than no navigation at all.
var (
	goFunc  = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`)
	goType  = regexp.MustCompile(`^type\s+([A-Za-z_]\w*)`)
	goDecl  = regexp.MustCompile(`^(?:var|const)\s+([A-Za-z_]\w*)`)
	goField = regexp.MustCompile(`^\t([A-Za-z_]\w*)\s+[\[\*A-Za-z_]`)

	pyDef    = regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+([A-Za-z_]\w*)`)
	pyClass  = regexp.MustCompile(`^(\s*)class\s+([A-Za-z_]\w*)`)
	pyAssign = regexp.MustCompile(`^([A-Za-z_]\w*)\s*(?::\s*[^=]+)?=[^=]`)

	cDefine  = regexp.MustCompile(`^\s*#\s*define\s+([A-Za-z_]\w*)`)
	cAggr    = regexp.MustCompile(`^\s*(?:typedef\s+)?(struct|union|enum)\s+([A-Za-z_]\w*)`)
	cTypedef = regexp.MustCompile(`^\s*typedef\s+.*?\b([A-Za-z_]\w*)\s*;`)
)

// cKeywords are control-flow constructs that look like calls. Without this the
// index fills up with entries named "if" and "while".
var cKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "return": true,
	"sizeof": true, "defined": true, "catch": true, "else": true, "do": true,
	"case": true, "goto": true, "typeof": true, "alignof": true,
	"static_assert": true, "_Static_assert": true, "assert": true,
}

func isIdentChar(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// cFunctionName extracts a function name from what looks like a C definition.
//
// The heuristic that carries most of the weight is indentation: in every common
// C style a function definition starts at column zero, while calls to it are
// indented inside a body. Combined with rejecting lines that end in a
// semicolon, that separates definitions from both calls and prototypes without
// needing to parse C.
func cFunctionName(line string) string {
	if line == "" {
		return ""
	}
	switch line[0] {
	case ' ', '\t', '#', '/', '*', '}', ')':
		return ""
	}
	open := strings.IndexByte(line, '(')
	if open <= 0 {
		return ""
	}
	end := open
	for end > 0 && line[end-1] == ' ' {
		end--
	}
	start := end
	for start > 0 && isIdentChar(line[start-1]) {
		start--
	}
	name := line[start:end]
	if name == "" || cKeywords[name] {
		return ""
	}
	if c := name[0]; !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
		return ""
	}
	// A return type must precede the name, or this is a bare call.
	if strings.TrimSpace(line[:start]) == "" {
		return ""
	}
	// A trailing semicolon marks a prototype rather than a definition.
	if t := strings.TrimRight(line, " \t"); strings.HasSuffix(t, ";") {
		return ""
	}
	return name
}

// found is one definition discovered while scanning a file.
type found struct {
	name string
	kind provider.Kind
	line int // 1-based
	text string
}

// scanLine extracts any definitions a single line declares.
func scanLine(lang Language, line string, num int) []found {
	var out []found
	add := func(name string, kind provider.Kind) {
		if name != "" {
			out = append(out, found{name: name, kind: kind, line: num, text: line})
		}
	}

	switch lang {
	case LangGo:
		switch {
		case strings.HasPrefix(line, "func "):
			if m := goFunc.FindStringSubmatch(line); m != nil {
				add(m[1], provider.KindFunction)
			}
		case strings.HasPrefix(line, "type "):
			if m := goType.FindStringSubmatch(line); m != nil {
				add(m[1], provider.KindTypedef)
			}
		case strings.HasPrefix(line, "var "), strings.HasPrefix(line, "const "):
			if m := goDecl.FindStringSubmatch(line); m != nil {
				add(m[1], provider.KindVariable)
			}
		case strings.HasPrefix(line, "\t"):
			// A single-tab indent inside a type block is a struct field.
			if m := goField.FindStringSubmatch(line); m != nil {
				add(m[1], provider.KindMember)
			}
		}

	case LangPython:
		if m := pyDef.FindStringSubmatch(line); m != nil {
			// An indented def is a method; both are worth finding.
			add(m[2], provider.KindFunction)
		} else if m := pyClass.FindStringSubmatch(line); m != nil {
			add(m[2], provider.KindClass)
		} else if m := pyAssign.FindStringSubmatch(line); m != nil {
			// Only module-level assignments; locals would swamp the index.
			add(m[1], provider.KindVariable)
		}

	case LangC:
		if m := cDefine.FindStringSubmatch(line); m != nil {
			add(m[1], provider.KindMacro)
			return out
		}
		if m := cAggr.FindStringSubmatch(line); m != nil {
			kind := provider.KindStruct
			switch m[1] {
			case "union":
				kind = provider.KindUnion
			case "enum":
				kind = provider.KindEnum
			}
			add(m[2], kind)
			// A line may both define an aggregate and typedef it.
		}
		if strings.HasPrefix(strings.TrimSpace(line), "typedef") {
			if m := cTypedef.FindStringSubmatch(line); m != nil {
				add(m[1], provider.KindTypedef)
			}
			return out
		}
		if name := cFunctionName(line); name != "" {
			add(name, provider.KindFunction)
		}
	}
	return out
}
