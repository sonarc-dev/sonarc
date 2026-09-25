// Package tags reads Exuberant/Universal ctags "tags" files.
//
// The defining constraint is size. A tags file for the Linux kernel runs to
// well over a gigabyte, and loading it would be unusable on the shared server
// this editor is meant for. So when the file declares itself sorted, lookups
// binary search the file on disk with ReadAt and never hold more than a few
// kilobytes at a time. Only small or unsorted files are read into memory.
package tags

import (
	"strconv"
	"strings"
)

// Entry is one line of a tags file.
type Entry struct {
	Name string
	File string // as written in the file, which may be relative

	// Exactly one of Line and Pattern locates the tag. ctags emits a pattern by
	// default, because a pattern still finds the definition after the file has
	// been edited, whereas a line number silently goes stale.
	Line    int
	Pattern string

	Kind      string // ctags kind letter, e.g. "f" for function
	Signature string // argument list, when ctags was run with --fields=+S
	Scope     string // enclosing struct/class/namespace
	Language  string
}

// parseEntry parses one tags line. It returns false for header lines and for
// anything malformed, so a corrupt file degrades to fewer results rather than
// to an error.
func parseEntry(line string) (Entry, bool) {
	if line == "" || line[0] == '!' {
		return Entry{}, false // header, e.g. !_TAG_FILE_SORTED
	}
	i := strings.IndexByte(line, '\t')
	if i <= 0 {
		return Entry{}, false
	}
	rest := line[i+1:]
	j := strings.IndexByte(rest, '\t')
	if j < 0 {
		return Entry{}, false
	}
	e := Entry{Name: line[:i], File: rest[:j]}

	addr, ext := splitAddress(rest[j+1:])
	if addr == "" {
		return Entry{}, false
	}
	if n, err := strconv.Atoi(addr); err == nil {
		e.Line = n
	} else {
		e.Pattern = unwrapPattern(addr)
	}
	parseFields(&e, ext)
	return e, true
}

// splitAddress separates the tag address from the extension fields.
//
// The address cannot simply be split on the next tab: a search pattern may
// contain tabs, and frequently does in indented code. So a pattern is scanned
// to its own closing delimiter, honoring backslash escapes.
func splitAddress(s string) (addr, ext string) {
	if s == "" {
		return "", ""
	}
	end := 0
	switch s[0] {
	case '/', '?':
		delim := s[0]
		i := 1
		for i < len(s) {
			if s[i] == '\\' {
				i += 2
				continue
			}
			if s[i] == delim {
				i++
				break
			}
			i++
		}
		end = i
	default:
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		end = i
	}
	addr = s[:end]
	ext = s[end:]
	// Extended format terminates the address with ;" before the fields.
	ext = strings.TrimPrefix(ext, ";\"")
	ext = strings.TrimPrefix(ext, "\t")
	return addr, ext
}

// unwrapPattern strips the delimiters and anchors from a ctags search pattern,
// leaving the literal text to look for.
func unwrapPattern(p string) string {
	if len(p) < 2 {
		return ""
	}
	p = p[1 : len(p)-1] // drop the / or ? delimiters
	p = strings.TrimPrefix(p, "^")
	p = strings.TrimSuffix(p, "$")
	// ctags escapes its delimiter and the escape character itself.
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' && i+1 < len(p) {
			switch p[i+1] {
			case '/', '?', '\\':
				i++
			}
		}
		b.WriteByte(p[i])
	}
	return b.String()
}

// parseFields reads the tab-separated extension fields.
func parseFields(e *Entry, ext string) {
	if ext == "" {
		return
	}
	for _, f := range strings.Split(ext, "\t") {
		if f == "" {
			continue
		}
		k, v, ok := strings.Cut(f, ":")
		if !ok {
			// A bare field is the kind, in the abbreviated form ctags emits
			// when --fields does not request explicit names.
			if e.Kind == "" {
				e.Kind = f
			}
			continue
		}
		switch k {
		case "kind":
			e.Kind = v
		case "line":
			if n, err := strconv.Atoi(v); err == nil {
				e.Line = n
			}
		case "signature":
			e.Signature = v
		case "language":
			e.Language = v
		case "file":
			// A static/file-scoped symbol; not a scope name.
		default:
			// Remaining namespaced fields are scopes: struct:, class:,
			// union:, enum:, function:, namespace: and so on.
			if e.Scope == "" && v != "" {
				e.Scope = v
			}
		}
	}
}

// entryName returns just the name field of a tags line, without parsing the
// rest. The binary search compares millions of these, so it must not allocate.
func entryName(line string) string {
	if i := strings.IndexByte(line, '\t'); i > 0 {
		return line[:i]
	}
	return ""
}
