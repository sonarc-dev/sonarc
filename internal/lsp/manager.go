package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// server describes a language server sonarc knows how to run.
type server struct {
	name  string
	argv  []string
	langs []string
	// needs, when set, says whether the project has what the server needs to
	// give useful answers; without it the server is not started.
	needs func(root string) (bool, string)
}

// defaults are the servers used when installed. clangd runs without its
// background index: on a kernel tree that index takes hours of CPU and
// gigabytes of disk, while cscope already answers references. It is started
// only where a compile_commands.json says how each file is built, since
// without one its answers for C are guesses.
var defaults = []server{
	{name: "gopls", argv: []string{"gopls"}, langs: []string{"go"}},
	{name: "clangd", argv: []string{"clangd", "--background-index=false", "--log=error"}, langs: []string{"c", "cpp"}, needs: compileCommands},
	{name: "rust-analyzer", argv: []string{"rust-analyzer"}, langs: []string{"rust"}},
	{name: "pyright", argv: []string{"pyright-langserver", "--stdio"}, langs: []string{"python"}},
	{name: "pylsp", argv: []string{"pylsp"}, langs: []string{"python"}},
	{name: "typescript-language-server", argv: []string{"typescript-language-server", "--stdio"}, langs: []string{"typescript", "javascript"}},
}

func compileCommands(root string) (bool, string) {
	for _, p := range []string{"compile_commands.json", "build/compile_commands.json"} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			return true, ""
		}
	}
	return false, "needs compile_commands.json"
}

// Languages are the names lsp.conf uses.
var languages = []string{"c", "cpp", "go", "python", "rust", "typescript", "javascript"}

// languageOf is the language of a file, by its extension, and the id the
// protocol uses for it.
func languageOf(path string) (lang, id string) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go", "go"
	case ".c", ".h":
		return "c", "c"
	case ".cc", ".cpp", ".cxx", ".c++", ".hpp", ".hh", ".hxx", ".inl":
		return "cpp", "cpp"
	case ".py", ".pyi":
		return "python", "python"
	case ".rs":
		return "rust", "rust"
	case ".ts":
		return "typescript", "typescript"
	case ".tsx":
		return "typescript", "typescriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript", "javascript"
	case ".jsx":
		return "javascript", "javascriptreact"
	}
	return "", ""
}

// entry is the server chosen for one or more languages, and its state.
type entry struct {
	name   string
	argv   []string // argv[0] resolved to a path
	why    string   // why it is not used, when it is not
	client *client
	start  chan struct{} // closed when a start attempt finishes
	err    error         // why the last start failed
	failed time.Time
}

// retryAfter is how long a server that failed to start or died is left
// alone before sonarc tries it again.
const retryAfter = time.Minute

// Manager runs the language servers for one project. It is a provider:
// position queries go to the server for the file's language, and a file in a
// language with no server is left to the other providers.
type Manager struct {
	root   string
	mu     sync.Mutex
	byLang map[string]*entry
	all    []*entry
}

// New chooses a server for each language from the defaults, installed
// programs and the lines of lsp.conf, if conf is not nil:
//
//	c clangd --background-index
//	python off
//
// It reports each line of lsp.conf it could not use.
func New(root string, conf io.Reader) (*Manager, []error) {
	m := &Manager{root: root, byLang: map[string]*entry{}}
	chosen := map[string][]string{} // lang → argv, from lsp.conf
	off := map[string]bool{}
	var errs []error
	if conf != nil {
		sc := bufio.NewScanner(conf)
		n := 0
		for sc.Scan() {
			n++
			f := strings.Fields(sc.Text())
			if len(f) == 0 || strings.HasPrefix(f[0], "#") {
				continue
			}
			lang := strings.ToLower(f[0])
			if !known(lang) {
				errs = append(errs, fmt.Errorf("line %d: unknown language %q; use one of %s", n, f[0], strings.Join(languages, ", ")))
				continue
			}
			if len(f) < 2 {
				errs = append(errs, fmt.Errorf("line %d: expected a command, or off, after %s", n, lang))
				continue
			}
			if len(f) == 2 && f[1] == "off" {
				off[lang] = true
				continue
			}
			chosen[lang] = f[1:]
		}
	}

	byArgv := map[string]*entry{}
	add := func(lang, name string, argv []string, why string) {
		key := strings.Join(argv, "\x00")
		e := byArgv[key]
		if e == nil {
			e = &entry{name: name, argv: argv, why: why}
			byArgv[key] = e
			m.all = append(m.all, e)
		}
		m.byLang[lang] = e
	}
	for _, lang := range languages {
		if off[lang] {
			continue
		}
		if argv, ok := chosen[lang]; ok {
			if path, err := lookPath(argv[0]); err == nil {
				add(lang, filepath.Base(argv[0]), append([]string{path}, argv[1:]...), "")
			} else {
				errs = append(errs, fmt.Errorf("%s: %s is not installed", lang, argv[0]))
			}
			continue
		}
		for _, s := range defaults {
			if !contains(s.langs, lang) {
				continue
			}
			path, err := lookPath(s.argv[0])
			if err != nil {
				continue // try the next server for the language
			}
			why := ""
			if s.needs != nil {
				if ok, reason := s.needs(root); !ok {
					why = reason
				}
			}
			add(lang, s.name, append([]string{path}, s.argv[1:]...), why)
			break
		}
	}
	return m, errs
}

func known(lang string) bool { return contains(languages, lang) }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// lookPath finds a program on PATH, then where Go, Cargo and pip put the
// programs they install: an ssh session's PATH often leaves those out.
func lookPath(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	if strings.ContainsRune(name, filepath.Separator) {
		return "", exec.ErrNotFound
	}
	home, _ := os.UserHomeDir()
	dirs := []string{filepath.Join(home, "go", "bin"), filepath.Join(home, ".cargo", "bin"), filepath.Join(home, ".local", "bin")}
	if gp := os.Getenv("GOPATH"); gp != "" {
		dirs = append([]string{filepath.Join(gp, "bin")}, dirs...)
	}
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// Name identifies the provider.
func (m *Manager) Name() string { return "lsp" }

// NameFor is the name of the server for path's language, such as gopls, for
// saying where an answer came from.
func (m *Manager) NameFor(path string) string {
	lang, _ := languageOf(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.byLang[lang]; e != nil {
		return e.name
	}
	return m.Name()
}

// Available reports whether any language has a server.
func (m *Manager) Available() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.all {
		if e.why == "" {
			return true
		}
	}
	return false
}

// Handles reports whether the file's language has a server that is running
// or may be started.
func (m *Manager) Handles(path string) bool {
	lang, _ := languageOf(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.byLang[lang]
	return e != nil && e.why == "" && (e.err == nil || time.Since(e.failed) > retryAfter)
}

// clientFor returns the running server for path, starting it if needed.
// Callers that arrive while it starts wait for that start rather than
// running a second server.
func (m *Manager) clientFor(ctx context.Context, path string) (*client, string, string, error) {
	lang, id := languageOf(path)
	m.mu.Lock()
	e := m.byLang[lang]
	if e == nil || e.why != "" {
		m.mu.Unlock()
		return nil, "", "", errNotStarted
	}
	for {
		if e.client != nil && e.client.alive() {
			c := e.client
			m.mu.Unlock()
			return c, id, e.name, nil
		}
		if e.start == nil {
			break
		}
		wait := e.start
		m.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, "", "", ctx.Err()
		}
		m.mu.Lock()
		if e.err != nil && time.Since(e.failed) <= retryAfter {
			err := e.err
			m.mu.Unlock()
			return nil, "", "", err
		}
	}
	if e.err != nil && time.Since(e.failed) <= retryAfter {
		err := e.err
		m.mu.Unlock()
		return nil, "", "", err
	}
	e.start = make(chan struct{})
	m.mu.Unlock()

	// Starting takes as long as the server needs to load the project, which
	// can outlast one query; it is not tied to the query's context, so a
	// cancelled lookup does not throw away a server that is nearly ready.
	startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	c, err := start(startCtx, e.argv, m.root)
	cancel()

	m.mu.Lock()
	e.client, e.err = c, err
	if err != nil {
		e.failed = time.Now()
	}
	close(e.start)
	e.start = nil
	m.mu.Unlock()
	if err != nil {
		return nil, "", "", fmt.Errorf("%s: %w", e.name, err)
	}
	return c, id, e.name, nil
}

// position is the protocol's position for at, after bringing the server up
// to date with the file.
func (m *Manager) prepare(ctx context.Context, at provider.At) (*client, map[string]any, string, error) {
	c, id, name, err := m.clientFor(ctx, at.Doc.Path)
	if err != nil {
		return nil, nil, "", err
	}
	if err := c.sync(at.Doc.Path, id, at.Doc.Version, at.Doc.Text); err != nil {
		return nil, nil, "", err
	}
	line := lineOf(at.Doc.Text, at.Line)
	params := map[string]any{
		"textDocument": map[string]string{"uri": fileURI(at.Doc.Path)},
		"position":     map[string]int{"line": at.Line, "character": c.character(line, at.Col)},
	}
	return c, params, name, nil
}

// DefinitionAt asks the file's server where the symbol at the position is
// defined.
func (m *Manager) DefinitionAt(ctx context.Context, at provider.At) ([]provider.Symbol, error) {
	c, params, name, err := m.prepare(ctx, at)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.call(ctx, "textDocument/definition", params, &raw); err != nil {
		return nil, err
	}
	locs := m.locations(c, raw, at.Doc)
	syms := make([]provider.Symbol, len(locs))
	for i, l := range locs {
		syms[i] = provider.Symbol{Loc: l, Source: name}
	}
	return syms, nil
}

// ReferencesAt asks the file's server for every use of the symbol at the
// position, its declaration included.
func (m *Manager) ReferencesAt(ctx context.Context, at provider.At) ([]provider.Location, error) {
	c, params, _, err := m.prepare(ctx, at)
	if err != nil {
		return nil, err
	}
	params["context"] = map[string]bool{"includeDeclaration": true}
	var raw json.RawMessage
	if err := c.call(ctx, "textDocument/references", params, &raw); err != nil {
		return nil, err
	}
	return m.locations(c, raw, at.Doc), nil
}

// HoverAt asks the file's server what the symbol at the position is, and
// makes one line of the answer: a signature where the server gives one.
func (m *Manager) HoverAt(ctx context.Context, at provider.At) (string, error) {
	c, params, _, err := m.prepare(ctx, at)
	if err != nil {
		return "", err
	}
	var res struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := c.call(ctx, "textDocument/hover", params, &res); err != nil {
		return "", err
	}
	return summarize(hoverText(res.Contents)), nil
}

// Definitions, References and Search by name are left to the indexes; a
// server is asked about positions.
func (m *Manager) Definitions(context.Context, string) ([]provider.Symbol, error) { return nil, nil }
func (m *Manager) References(context.Context, string) ([]provider.Location, error) {
	return nil, nil
}
func (m *Manager) Search(context.Context, string, int) ([]provider.Symbol, error) { return nil, nil }

// Close stops every server.
func (m *Manager) Close() error {
	m.mu.Lock()
	var cs []*client
	for _, e := range m.all {
		if e.client != nil {
			cs = append(cs, e.client)
			e.client = nil
		}
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range cs {
		wg.Add(1)
		go func(c *client) { defer wg.Done(); c.shutdown() }(c)
	}
	wg.Wait()
	return nil
}

// Status describes each server, for Ctrl+K ?: running, not started yet,
// failed, or not used and why.
func (m *Manager) Status() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	langs := map[*entry][]string{}
	for lang, e := range m.byLang {
		langs[e] = append(langs[e], lang)
	}
	var out []string
	for _, e := range m.all {
		ls := langs[e]
		sort.Strings(ls)
		state := "starts when a file needs it"
		switch {
		case e.why != "":
			state = "not used: " + e.why
		case e.client != nil && e.client.alive():
			state = "running"
		case e.start != nil:
			state = "starting"
		case e.err != nil:
			state = "failed: " + e.err.Error()
		}
		out = append(out, fmt.Sprintf("%s (%s): %s", e.name, strings.Join(ls, ", "), state))
	}
	return out
}

// locations converts a definition or references answer, which may be one
// Location, a list of them, or a list of LocationLinks, into sonarc's own,
// with each line's text for the results list.
func (m *Manager) locations(c *client, raw json.RawMessage, doc provider.Doc) []provider.Location {
	type pos struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	}
	type rng struct {
		Start pos `json:"start"`
	}
	type loc struct {
		URI                  string `json:"uri"`
		Range                *rng   `json:"range"`
		TargetURI            string `json:"targetUri"`
		TargetSelectionRange *rng   `json:"targetSelectionRange"`
	}
	var list []loc
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if raw[0] == '[' {
		if json.Unmarshal(raw, &list) != nil {
			return nil
		}
	} else {
		var one loc
		if json.Unmarshal(raw, &one) != nil {
			return nil
		}
		list = []loc{one}
	}

	files := map[string][]byte{doc.Path: doc.Text}
	var out []provider.Location
	for _, l := range list {
		uri, r := l.URI, l.Range
		if l.TargetURI != "" {
			uri, r = l.TargetURI, l.TargetSelectionRange
		}
		path := uriPath(uri)
		if path == "" || r == nil {
			continue
		}
		text, ok := files[path]
		if !ok {
			text, _ = os.ReadFile(path)
			files[path] = text
		}
		line := lineOf(text, r.Start.Line)
		out = append(out, provider.Location{
			Path: path,
			Line: r.Start.Line + 1,
			Col:  c.byteCol(line, r.Start.Character) + 1,
			Text: strings.TrimSpace(string(line)),
		})
	}
	return out
}

// hoverText extracts the text of a hover answer, which may be MarkupContent,
// a MarkedString, or a list of MarkedStrings.
func hoverText(raw json.RawMessage) string {
	var markup struct {
		Kind     string `json:"kind"`
		Value    string `json:"value"`
		Language string `json:"language"`
	}
	var s string
	var list []json.RawMessage
	switch {
	case json.Unmarshal(raw, &s) == nil:
		return s
	case json.Unmarshal(raw, &list) == nil:
		var parts []string
		for _, r := range list {
			if t := hoverText(r); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n\n")
	case json.Unmarshal(raw, &markup) == nil:
		if markup.Language != "" {
			return "```" + markup.Language + "\n" + markup.Value + "\n```"
		}
		return markup.Value
	}
	return ""
}

var fence = regexp.MustCompile("(?s)```[a-zA-Z0-9_+-]*\n(.*?)```")

// summarize makes a hover answer fit the message line: the first code block,
// which is where servers put a signature, or else the first paragraph, with
// whitespace collapsed.
func summarize(text string) string {
	if m := fence.FindStringSubmatch(text); m != nil {
		text = m[1]
	} else if i := strings.Index(text, "\n\n"); i >= 0 {
		text = text[:i]
	}
	text = strings.Join(strings.Fields(text), " ")
	const limit = 240
	if len(text) > limit {
		text = text[:limit] + "…"
	}
	return text
}

var _ provider.CodeIntel = (*Manager)(nil)
var _ provider.PositionIntel = (*Manager)(nil)
