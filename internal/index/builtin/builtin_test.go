package builtin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sonarc/internal/index/provider"
)

// tree writes a set of files under a temp dir and returns its path.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func buildIndex(t *testing.T, root string) *Index {
	t.Helper()
	ix := New(root)
	if err := ix.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return ix
}

// defNames returns the names of every definition found for sym.
func defLines(t *testing.T, ix *Index, sym string) []int {
	t.Helper()
	syms, err := ix.Definitions(context.Background(), sym)
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, s := range syms {
		out = append(out, s.Loc.Line)
	}
	return out
}

func TestIndexesCDefinitions(t *testing.T) {
	root := tree(t, map[string]string{
		"src/main.c": `#include <stdio.h>

#define MAX_SIZE 1024

struct config {
	int flags;
};

typedef struct config config_t;

static int helper(int x)
{
	return x * 2;
}

int main(int argc, char **argv)
{
	if (argc > 1) {
		helper(argc);
	}
	return 0;
}
`,
	})
	ix := buildIndex(t, root)

	for _, tc := range []struct {
		sym  string
		kind provider.Kind
	}{
		{"MAX_SIZE", provider.KindMacro},
		{"config", provider.KindStruct},
		{"helper", provider.KindFunction},
		{"main", provider.KindFunction},
	} {
		syms, err := ix.Definitions(context.Background(), tc.sym)
		if err != nil {
			t.Fatal(err)
		}
		if len(syms) == 0 {
			t.Errorf("%s was not indexed", tc.sym)
			continue
		}
		if syms[0].Kind != tc.kind {
			t.Errorf("%s kind = %v, want %v", tc.sym, syms[0].Kind, tc.kind)
		}
	}
}

// The indentation heuristic is what separates definitions from calls. If this
// breaks, every call site is reported as a definition.
func TestCCallsAreNotDefinitions(t *testing.T) {
	root := tree(t, map[string]string{
		"a.c": `int real_def(void)
{
	other_function(1, 2);
	return nested_call(x);
}
`,
	})
	ix := buildIndex(t, root)

	if got := defLines(t, ix, "real_def"); len(got) != 1 {
		t.Errorf("real_def: got %v definitions, want exactly 1", got)
	}
	for _, name := range []string{"other_function", "nested_call"} {
		if got := defLines(t, ix, name); len(got) != 0 {
			t.Errorf("%s was indexed as a definition at lines %v, but it is a call", name, got)
		}
	}
}

// A prototype is a declaration, not a definition; jumping to it instead of the
// body is the single most annoying way a C index can be wrong.
func TestCPrototypesAreNotDefinitions(t *testing.T) {
	root := tree(t, map[string]string{
		"h.h": "int declared_only(int x);\nvoid another(void);\n",
		"c.c": "int declared_only(int x)\n{\n\treturn x;\n}\n",
	})
	ix := buildIndex(t, root)

	syms, err := ix.Definitions(context.Background(), "declared_only")
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 {
		t.Fatalf("got %d definitions, want 1 (the body, not the prototype)", len(syms))
	}
	if !strings.HasSuffix(syms[0].Loc.Path, "c.c") {
		t.Errorf("definition points at %s, want the .c file", syms[0].Loc.Path)
	}
	if got := defLines(t, ix, "another"); len(got) != 0 {
		t.Errorf("a bare prototype was indexed as a definition at %v", got)
	}
}

func TestCControlFlowIsNotIndexed(t *testing.T) {
	root := tree(t, map[string]string{
		"a.c": "int f(void)\n{\n\treturn 0;\n}\n",
	})
	ix := buildIndex(t, root)
	for _, kw := range []string{"if", "for", "while", "switch", "return", "sizeof"} {
		if got := defLines(t, ix, kw); len(got) != 0 {
			t.Errorf("keyword %q was indexed as a symbol", kw)
		}
	}
}

func TestIndexesGoDefinitions(t *testing.T) {
	root := tree(t, map[string]string{
		"pkg/thing.go": `package pkg

type Thing struct {
	Name string
}

const Limit = 10

var Registry map[string]Thing

func New(name string) *Thing {
	return &Thing{Name: name}
}

func (t *Thing) Describe() string {
	return t.Name
}
`,
	})
	ix := buildIndex(t, root)

	for _, tc := range []struct {
		sym  string
		kind provider.Kind
	}{
		{"Thing", provider.KindTypedef},
		{"Limit", provider.KindVariable},
		{"Registry", provider.KindVariable},
		{"New", provider.KindFunction},
		{"Describe", provider.KindFunction}, // method with a receiver
		{"Name", provider.KindMember},       // struct field
	} {
		syms, _ := ix.Definitions(context.Background(), tc.sym)
		if len(syms) == 0 {
			t.Errorf("%s was not indexed", tc.sym)
			continue
		}
		if syms[0].Kind != tc.kind {
			t.Errorf("%s kind = %v, want %v", tc.sym, syms[0].Kind, tc.kind)
		}
	}
}

func TestIndexesPythonDefinitions(t *testing.T) {
	root := tree(t, map[string]string{
		"app.py": `TIMEOUT = 30

class Handler:
    def handle(self, req):
        return None

    async def handle_async(self, req):
        return None

def main():
    pass
`,
	})
	ix := buildIndex(t, root)

	for _, tc := range []struct {
		sym  string
		kind provider.Kind
	}{
		{"TIMEOUT", provider.KindVariable},
		{"Handler", provider.KindClass},
		{"handle", provider.KindFunction},
		{"handle_async", provider.KindFunction}, // async def
		{"main", provider.KindFunction},
	} {
		syms, _ := ix.Definitions(context.Background(), tc.sym)
		if len(syms) == 0 {
			t.Errorf("%s was not indexed", tc.sym)
			continue
		}
		if syms[0].Kind != tc.kind {
			t.Errorf("%s kind = %v, want %v", tc.sym, syms[0].Kind, tc.kind)
		}
	}
}

// Searching for "read" must not report "thread" or "readv".
func TestReferencesMatchWholeWordsOnly(t *testing.T) {
	root := tree(t, map[string]string{
		"a.c": "int read(void);\nint thread_start(void);\nint readv(void);\ncall read();\nx = pread(0);\n",
	})
	ix := buildIndex(t, root)

	refs, err := ix.References(context.Background(), "read")
	if err != nil {
		t.Fatal(err)
	}
	wantLines := map[int]bool{1: true, 4: true}
	if len(refs) != len(wantLines) {
		t.Fatalf("got %d references at %v, want lines 1 and 4", len(refs), refs)
	}
	for _, r := range refs {
		if !wantLines[r.Line] {
			t.Errorf("unexpected reference on line %d: %q", r.Line, r.Text)
		}
	}
}

func TestReferencesAcrossFiles(t *testing.T) {
	root := tree(t, map[string]string{
		"a.c":     "void target(void) {}\n",
		"b.c":     "void caller(void) { target(); }\n",
		"sub/c.c": "void other(void) { target(); }\n",
	})
	ix := buildIndex(t, root)

	refs, err := ix.References(context.Background(), "target")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 3 {
		t.Fatalf("got %d references, want 3: %+v", len(refs), refs)
	}
	// Results must be sorted by path so the panel reads coherently.
	for i := 1; i < len(refs); i++ {
		if refs[i-1].Path > refs[i].Path {
			t.Errorf("results are not sorted: %s before %s", refs[i-1].Path, refs[i].Path)
		}
	}
}

func TestSearchRanksShortExactPrefixesFirst(t *testing.T) {
	root := tree(t, map[string]string{
		"a.c": "int read_the_whole_buffer(void)\n{\n\treturn 0;\n}\n" +
			"int read(void)\n{\n\treturn 0;\n}\n" +
			"int read_buf(void)\n{\n\treturn 0;\n}\n",
	})
	ix := buildIndex(t, root)

	got, err := ix.Search(context.Background(), "read", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no results")
	}
	if got[0].Name != "read" {
		t.Errorf("first result = %q, want the exact short match %q", got[0].Name, "read")
	}
}

func TestGitignoreIsRespected(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore":      "build/\n*.gen.c\nsecret.c\n",
		"keep.c":          "int keep_me(void)\n{\n\treturn 0;\n}\n",
		"build/ignored.c": "int ignored_build(void)\n{\n\treturn 0;\n}\n",
		"thing.gen.c":     "int ignored_gen(void)\n{\n\treturn 0;\n}\n",
		"secret.c":        "int ignored_secret(void)\n{\n\treturn 0;\n}\n",
	})
	ix := buildIndex(t, root)

	if got := defLines(t, ix, "keep_me"); len(got) != 1 {
		t.Errorf("keep_me should be indexed, got %v", got)
	}
	for _, name := range []string{"ignored_build", "ignored_gen", "ignored_secret"} {
		if got := defLines(t, ix, name); len(got) != 0 {
			t.Errorf("%s is gitignored but was indexed at %v", name, got)
		}
	}
}

func TestSkipsVcsAndDependencyDirs(t *testing.T) {
	root := tree(t, map[string]string{
		"main.c":               "int real(void)\n{\n\treturn 0;\n}\n",
		".git/hooks/x.c":       "int in_git(void)\n{\n\treturn 0;\n}\n",
		"node_modules/pkg/a.c": "int in_modules(void)\n{\n\treturn 0;\n}\n",
		"vendor/lib/b.c":       "int in_vendor(void)\n{\n\treturn 0;\n}\n",
		"__pycache__/c.py":     "def in_pycache(): pass\n",
	})
	ix := buildIndex(t, root)

	if got := defLines(t, ix, "real"); len(got) != 1 {
		t.Errorf("real should be indexed, got %v", got)
	}
	for _, name := range []string{"in_git", "in_modules", "in_vendor", "in_pycache"} {
		if got := defLines(t, ix, name); len(got) != 0 {
			t.Errorf("%s is in a skipped directory but was indexed at %v", name, got)
		}
	}
}

func TestBinaryFilesAreSkipped(t *testing.T) {
	root := t.TempDir()
	// A .c file that is actually binary, which is what generated or corrupt
	// files in a tree look like.
	if err := os.WriteFile(filepath.Join(root, "blob.c"),
		append([]byte("int fake(void)\n{\n"), 0x00, 0x01, 0x02), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := buildIndex(t, root)
	if got := defLines(t, ix, "fake"); len(got) != 0 {
		t.Errorf("a binary file was parsed: %v", got)
	}
}

func TestUnbuiltIndexAnswersEmptyNotError(t *testing.T) {
	ix := New(t.TempDir())
	if ix.Available() {
		t.Error("a fresh index should not report itself available")
	}
	syms, err := ix.Definitions(context.Background(), "anything")
	if err != nil || len(syms) != 0 {
		t.Errorf("unbuilt index returned %v, %v; want empty and no error", syms, err)
	}
	refs, err := ix.References(context.Background(), "anything")
	if err != nil || len(refs) != 0 {
		t.Errorf("unbuilt index returned %v, %v; want empty and no error", refs, err)
	}
}

func TestBackgroundBuild(t *testing.T) {
	root := tree(t, map[string]string{"a.c": "int bg(void)\n{\n\treturn 0;\n}\n"})
	ix := New(root)
	ix.Start()
	ix.Wait()

	if !ix.Available() {
		t.Fatal("index did not become available after the background build")
	}
	if got := defLines(t, ix, "bg"); len(got) != 1 {
		t.Errorf("got %v, want one definition", got)
	}
	files, symbols := ix.Stats()
	if files == 0 || symbols == 0 {
		t.Errorf("Stats reported %d files, %d symbols", files, symbols)
	}
}

// The index must survive being pointed at a real, messy source tree: this
// project's own, which mixes Go, Markdown and Makefiles.
func TestIndexesThisRepository(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skip("not running from the repository")
	}
	ix := buildIndex(t, root)

	files, symbols := ix.Stats()
	if files < 5 || symbols < 20 {
		t.Fatalf("indexed only %d files and %d symbols from the repo", files, symbols)
	}
	// A symbol known to exist in this codebase.
	syms, _ := ix.Definitions(context.Background(), "Iterate")
	if len(syms) == 0 {
		t.Error("did not find the Iterate function in internal/text")
	}
	for _, s := range syms {
		if !strings.HasSuffix(s.Loc.Path, ".go") {
			t.Errorf("Iterate found in unexpected file %s", s.Loc.Path)
		}
	}
}

func BenchmarkGrep(b *testing.B) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		b.Skip(err)
	}
	ix := New(root)
	if err := ix.Build(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := ix.References(context.Background(), "Buffer"); err != nil {
			b.Fatal(err)
		}
	}
}

// Indexing a kernel-sized tree retained over 2 GB of symbol strings, enough to
// drive a 4 GB server to the edge of OOM, for definitions cscope and ctags were
// already answering. SkipSymbols keeps the file list -- which fuzzy open and
// project text search still need -- and drops only the symbol map.
func TestSkipSymbolsKeepsFilesButDropsSymbols(t *testing.T) {
	root := tree(t, map[string]string{
		"a.c": "int alpha(void)\n{\n\treturn 1;\n}\n",
		"b.c": "int beta(void)\n{\n\treturn 2;\n}\n",
	})

	ix := New(root)
	ix.SkipSymbols()
	if err := ix.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	if got := len(ix.Files()); got != 2 {
		t.Errorf("Files() = %d, want 2: the file list must survive", got)
	}
	if defs := defLines(t, ix, "alpha"); len(defs) != 0 {
		t.Errorf("Definitions(alpha) = %v, want none when symbols are skipped", defs)
	}

	// Project-wide text search must still work, since it reads files directly.
	hits, err := ix.Grep(context.Background(), "beta", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("Grep found nothing; project search must work without the symbol map")
	}
}

// The default path must be unaffected: with no stronger provider available the
// built-in index is the only thing answering, so it still extracts symbols.
func TestSymbolsAreBuiltByDefault(t *testing.T) {
	root := tree(t, map[string]string{
		"a.c": "int alpha(void)\n{\n\treturn 1;\n}\n",
	})
	ix := buildIndex(t, root)
	if defs := defLines(t, ix, "alpha"); len(defs) == 0 {
		t.Error("Definitions(alpha) empty; symbols must be built by default")
	}
}

// A scan that does not finish must say how far it got, not just that it failed.
func TestGrepReportsHowMuchWasCoveredWhenCutShort(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 40; i++ {
		files[fmt.Sprintf("f%02d.c", i)] = "int target;\n"
	}
	ix := buildIndex(t, tree(t, files))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ix.Grep(ctx, "target", true)

	var pe *provider.PartialError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a *provider.PartialError", err)
	}
	if pe.Total != 40 {
		t.Errorf("Total = %d, want 40", pe.Total)
	}
	if pe.Done >= pe.Total {
		t.Errorf("Done = %d of %d: nothing should have completed on a dead context", pe.Done, pe.Total)
	}
	if !errors.Is(err, context.Canceled) {
		t.Error("the error must still unwrap to the context error")
	}
}

func TestBuiltinIsTreatedAsAWholeTreeScanner(t *testing.T) {
	var p provider.CodeIntel = New(t.TempDir())
	rs, ok := p.(provider.ReferenceScanner)
	if !ok || !rs.ScansForReferences() {
		t.Error("the built-in indexer reads every file to find references; the Registry must know")
	}
}
