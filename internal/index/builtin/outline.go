package builtin

import (
	"strings"

	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// Outline lists the definitions in one file, in order, from its current
// contents: n lines, read through line. It works on what is in the editor, so
// the outline of a file being written is never stale.
//
// It uses the same heuristics as the index, with one addition for C. The
// index is searched by name, so a "struct inode *inode;" local that matches
// the aggregate pattern costs nothing there; in an outline, every such local
// would be a line of noise. So a struct, union or enum only counts when it
// starts at column zero and is not a declaration or parameter.
func Outline(path string, n int, line func(int) []byte) []provider.Symbol {
	lang, ok := languageOf(path)
	if !ok || lang == LangUnknown {
		return nil
	}
	var out []provider.Symbol
	for i := range n {
		text := strings.TrimRight(string(line(i)), "\r")
		for _, f := range scanLine(lang, text, i+1) {
			if lang == LangC && isAggregate(f.kind) && !aggregateDefinition(text) {
				continue
			}
			out = append(out, provider.Symbol{
				Name:   f.name,
				Kind:   f.kind,
				Source: "outline",
				Loc:    provider.Location{Path: path, Line: f.line, Text: strings.TrimSpace(f.text)},
			})
		}
	}
	return out
}

func isAggregate(k provider.Kind) bool {
	return k == provider.KindStruct || k == provider.KindUnion || k == provider.KindEnum
}

func aggregateDefinition(line string) bool {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return false
	}
	if strings.Contains(line, "{") {
		return true // "struct item {", or a whole "enum state { A, B };"
	}
	t := strings.TrimRight(line, " \t")
	return !strings.HasSuffix(t, ";") && !strings.Contains(t, "(")
}
