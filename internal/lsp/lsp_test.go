package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// The test binary doubles as a fake language server: run with
// SONARC_FAKE_LSP set, it serves the protocol on stdin and stdout instead of
// running tests. SONARC_FAKE_LSP=utf-16 makes it keep the protocol's default
// column unit, to exercise the conversion.
func TestMain(m *testing.M) {
	if mode := os.Getenv("SONARC_FAKE_LSP"); mode != "" {
		fakeServer(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeServer(mode string) {
	in := bufio.NewReader(os.Stdin)
	docs := map[string]string{}
	utf8 := mode != "utf-16"
	reply := func(id *json.RawMessage, result any) {
		writeMessage(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	// Positions in the fake are in the unit it negotiated.
	toChar := func(line string, byteCol int) int {
		if utf8 {
			return byteCol
		}
		return utf16Len([]byte(line[:byteCol]))
	}
	fromChar := func(line string, ch int) int {
		c := &client{utf8: utf8}
		return c.byteCol([]byte(line), ch)
	}
	wordAt := func(uri string, line, ch int) string {
		ls := strings.Split(docs[uri], "\n")
		if line >= len(ls) {
			return ""
		}
		l := ls[line]
		col := fromChar(l, ch)
		start, end := col, col
		for start > 0 && provider.IsSymbolChar(l[start-1]) {
			start--
		}
		for end < len(l) && provider.IsSymbolChar(l[end]) {
			end++
		}
		return l[start:end]
	}
	find := func(uri, pattern string) []map[string]any {
		var out []map[string]any
		re := regexp.MustCompile(pattern)
		for i, l := range strings.Split(docs[uri], "\n") {
			for _, m := range re.FindAllStringSubmatchIndex(l, -1) {
				at := m[0]
				if len(m) > 2 && m[2] >= 0 {
					at = m[2]
				}
				out = append(out, map[string]any{"uri": uri, "range": map[string]any{
					"start": map[string]int{"line": i, "character": toChar(l, at)},
					"end":   map[string]int{"line": i, "character": toChar(l, at)},
				}})
			}
		}
		return out
	}
	for {
		m, err := readMessage(in)
		if err != nil {
			return
		}
		var p struct {
			TextDocument struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"textDocument"`
			Position struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"position"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}
		json.Unmarshal(m.Params, &p)
		switch m.Method {
		case "initialize":
			caps := map[string]any{}
			if utf8 {
				caps["positionEncoding"] = "utf-8"
			}
			reply(m.ID, map[string]any{"capabilities": caps})
		case "textDocument/didOpen":
			docs[p.TextDocument.URI] = p.TextDocument.Text
		case "textDocument/didChange":
			docs[p.TextDocument.URI] = p.ContentChanges[0].Text
		case "textDocument/definition":
			// Ask the client something first, as real servers do; a client
			// that does not answer would hang here.
			writeMessage(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": 999, "method": "workspace/configuration",
				"params": map[string]any{"items": []any{map[string]any{}}}})
			if a, err := readMessage(in); err != nil || a.ID == nil || string(*a.ID) != "999" {
				return
			}
			w := wordAt(p.TextDocument.URI, p.Position.Line, p.Position.Character)
			reply(m.ID, find(p.TextDocument.URI, `func (`+regexp.QuoteMeta(w)+`)\b`))
		case "textDocument/references":
			w := wordAt(p.TextDocument.URI, p.Position.Line, p.Position.Character)
			reply(m.ID, find(p.TextDocument.URI, `\b`+regexp.QuoteMeta(w)+`\b`))
		case "textDocument/hover":
			w := wordAt(p.TextDocument.URI, p.Position.Line, p.Position.Character)
			reply(m.ID, map[string]any{"contents": map[string]string{"kind": "markdown",
				"value": "```go\nfunc " + w + "(\n\tn int,\n) error\n```\n\n" + w + " does a thing."}})
		case "shutdown":
			reply(m.ID, nil)
		case "exit":
			return
		}
	}
}

// fake returns a Manager whose Go server is this test binary.
func fake(t *testing.T, mode string) (*Manager, string) {
	t.Helper()
	t.Setenv("SONARC_FAKE_LSP", mode)
	root := t.TempDir()
	m, errs := New(root, strings.NewReader("go "+os.Args[0]+"\n"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	t.Cleanup(func() { m.Close() })
	return m, root
}

func at(path, text string, line, col int, version uint64) provider.At {
	return provider.At{Doc: provider.Doc{Path: path, Version: version, Text: []byte(text)}, Line: line, Col: col}
}

const goSrc = "package p\n\n// 日本 é\nfunc helper() {}\n\nfunc main() {\n\t/* 😀 */ helper()\n}\n"

func TestDefinitionThroughAServer(t *testing.T) {
	for _, mode := range []string{"utf-8", "utf-16"} {
		t.Run(mode, func(t *testing.T) {
			m, root := fake(t, mode)
			path := filepath.Join(root, "p.go")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// The call on line 7 sits after an emoji, two UTF-16 units but
			// four bytes: a wrong conversion lands on the wrong word.
			col := strings.Index(strings.Split(goSrc, "\n")[6], "helper")
			syms, err := m.DefinitionAt(ctx, at(path, goSrc, 6, col+2, 1))
			if err != nil {
				t.Fatal(err)
			}
			if len(syms) != 1 || syms[0].Loc.Line != 4 || syms[0].Loc.Col != 6 || syms[0].Loc.Text != "func helper() {}" {
				t.Fatalf("definition = %+v, want p.go:4:6", syms)
			}
		})
	}
}

// Unsaved edits reach the server before it is asked anything.
func TestServerSeesUnsavedEdits(t *testing.T) {
	m, root := fake(t, "utf-8")
	path := filepath.Join(root, "p.go")
	ctx := context.Background()
	src := "package p\n\nfunc a() { a(); a() }\n"
	locs, err := m.ReferencesAt(ctx, at(path, src, 2, 5, 1))
	if err != nil || len(locs) != 3 {
		t.Fatalf("references = %v, %v; want 3", locs, err)
	}
	src += "func b() { a() }\n"
	locs, _ = m.ReferencesAt(ctx, at(path, src, 2, 5, 2))
	if len(locs) != 4 {
		t.Errorf("after an edit: %d references, want 4 (the server was not told)", len(locs))
	}
}

func TestHoverIsOneLine(t *testing.T) {
	m, root := fake(t, "utf-8")
	got, err := m.HoverAt(context.Background(), at(filepath.Join(root, "p.go"), goSrc, 3, 6, 1))
	if err != nil || got != "func helper( n int, ) error" {
		t.Errorf("hover = %q, %v", got, err)
	}
}

func TestStatusAndShutdown(t *testing.T) {
	m, root := fake(t, "utf-8")
	if s := strings.Join(m.Status(), "\n"); !strings.Contains(s, "starts when a file needs it") {
		t.Errorf("status before use = %q", s)
	}
	if _, err := m.DefinitionAt(context.Background(), at(filepath.Join(root, "p.go"), goSrc, 3, 6, 1)); err != nil {
		t.Fatal(err)
	}
	if s := strings.Join(m.Status(), "\n"); !strings.Contains(s, "running") {
		t.Errorf("status after use = %q", s)
	}
	c := m.byLang["go"].client
	m.Close()
	if c.cmd.ProcessState == nil {
		t.Error("the server process was not waited for")
	}
}

// A server that cannot start leaves the language to the other providers for
// a while instead of failing every query.
func TestAFailedServerStepsAside(t *testing.T) {
	root := t.TempDir()
	m, _ := New(root, strings.NewReader("go sh -c exit\n")) // a server that exits at once
	defer m.Close()
	path := filepath.Join(root, "p.go")
	if !m.Handles(path) {
		t.Fatal("the server should be tried the first time")
	}
	if _, err := m.DefinitionAt(context.Background(), at(path, goSrc, 3, 6, 1)); err == nil {
		t.Fatal("a server that exits at once should fail")
	}
	if m.Handles(path) {
		t.Error("a server that just failed should not be asked again straight away")
	}
	if s := strings.Join(m.Status(), "\n"); !strings.Contains(s, "failed") {
		t.Errorf("status = %q", s)
	}
}

func TestConfiguration(t *testing.T) {
	root := t.TempDir()
	m, errs := New(root, strings.NewReader(`# comment
go off
cobol cobold
rust
python /no/such/server
`))
	if _, ok := m.byLang["go"]; ok {
		t.Error("go off should leave Go without a server")
	}
	want := []string{"line 3: unknown language", "line 4: expected a command", "python: /no/such/server is not installed"}
	if len(errs) != len(want) {
		t.Fatalf("errors = %v", errs)
	}
	for i, w := range want {
		if !strings.Contains(errs[i].Error(), w) {
			t.Errorf("error %d = %q, want %q", i, errs[i], w)
		}
	}
}

// clangd is only used where compile_commands.json says how files are built.
func TestClangdNeedsCompileCommands(t *testing.T) {
	if _, err := lookPath("clangd"); err != nil {
		t.Skip("clangd is not installed")
	}
	root := t.TempDir()
	m, _ := New(root, nil)
	if m.Handles(filepath.Join(root, "a.c")) {
		t.Error("clangd would start without compile_commands.json")
	}
	os.WriteFile(filepath.Join(root, "compile_commands.json"), []byte("[]"), 0o644)
	m, _ = New(root, nil)
	if !m.Handles(filepath.Join(root, "a.c")) {
		t.Error("clangd should be used once compile_commands.json exists")
	}
}

func TestSummarize(t *testing.T) {
	for in, want := range map[string]string{
		"```c\nint  open(const char *path,\n    int flags)\n```\nOpens a file.": "int open(const char *path, int flags)",
		"A plain paragraph.\n\nAnd another.":                                    "A plain paragraph.",
		"":                                                                      "",
	} {
		if got := summarize(in); got != want {
			t.Errorf("summarize(%q) = %q, want %q", in, got, want)
		}
	}
	if got := hoverText(json.RawMessage(`[{"language":"go","value":"func f()"},"doc"]`)); !strings.Contains(got, "func f()") {
		t.Errorf("hoverText of MarkedString list = %q", got)
	}
}

func TestFraming(t *testing.T) {
	var buf bytes.Buffer
	writeMessage(&buf, map[string]any{"jsonrpc": "2.0", "method": "x", "params": map[string]int{"a": 1}})
	m, err := readMessage(bufio.NewReader(&buf))
	if err != nil || m.Method != "x" || string(m.Params) != `{"a":1}` {
		t.Fatalf("round trip = %+v, %v", m, err)
	}
	if _, err := readMessage(bufio.NewReader(strings.NewReader("Content-Length: 99999999999\r\n\r\n"))); err == nil {
		t.Error("an absurd Content-Length should be refused")
	}
}

// With gopls installed, a real server answers across files in a module.
func TestRealGopls(t *testing.T) {
	gopls, err := lookPath("gopls")
	if err != nil {
		t.Skip("gopls is not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(root, "util.go"), []byte("package m\n\n// Twice doubles n.\nfunc Twice(n int) int { return 2 * n }\n"), 0o644)
	main := "package m\n\nfunc use() int { return Twice(21) }\n"
	os.WriteFile(filepath.Join(root, "main.go"), []byte(main), 0o644)

	m, _ := New(root, strings.NewReader("go "+gopls+"\n"))
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	col := strings.Index(main, "Twice") - strings.Index(main, "func use")
	syms, err := m.DefinitionAt(ctx, at(filepath.Join(root, "main.go"), main, 2, col+1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 || filepath.Base(syms[0].Loc.Path) != "util.go" || syms[0].Loc.Line != 4 {
		t.Fatalf("gopls definition = %+v, want util.go:4", syms)
	}
	hover, _ := m.HoverAt(ctx, at(filepath.Join(root, "main.go"), main, 2, col+1, 1))
	if hover != "func Twice(n int) int" {
		t.Errorf("gopls hover = %q, want just the signature", hover)
	}
	if m.NameFor(filepath.Join(root, "main.go")) != "gopls" {
		t.Errorf("NameFor = %q", m.NameFor(filepath.Join(root, "main.go")))
	}
}
