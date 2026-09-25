package tags

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestParseEntry(t *testing.T) {
	tests := []struct {
		name string
		line string
		want Entry
	}{
		{
			name: "line number address",
			line: "main\tsrc/main.c\t42;\"\tf",
			want: Entry{Name: "main", File: "src/main.c", Line: 42, Kind: "f"},
		},
		{
			name: "pattern address",
			line: "vfs_read\tfs/read_write.c\t/^ssize_t vfs_read(void)$/;\"\tf",
			want: Entry{Name: "vfs_read", File: "fs/read_write.c",
				Pattern: "ssize_t vfs_read(void)", Kind: "f"},
		},
		{
			name: "old format with no extension fields",
			line: "foo\tbar.c\t/^foo$/",
			want: Entry{Name: "foo", File: "bar.c", Pattern: "foo"},
		},
		{
			name: "numeric address without extensions",
			line: "foo\tbar.c\t17",
			want: Entry{Name: "foo", File: "bar.c", Line: 17},
		},
		{
			name: "named kind and line fields",
			line: "Point\tgeom.go\t/^type Point struct$/;\"\tkind:struct\tline:9",
			want: Entry{Name: "Point", File: "geom.go", Pattern: "type Point struct",
				Kind: "struct", Line: 9},
		},
		{
			name: "scope and signature",
			line: "read\tio.c\t/^read$/;\"\tf\tstruct:file_ops\tsignature:(int fd)",
			want: Entry{Name: "read", File: "io.c", Pattern: "read", Kind: "f",
				Scope: "file_ops", Signature: "(int fd)"},
		},
		{
			// A pattern may contain tabs, which is why the address cannot be
			// found by splitting on tab.
			name: "pattern containing a tab",
			line: "indented\tf.c\t/^\tint indented;$/;\"\tv",
			want: Entry{Name: "indented", File: "f.c", Pattern: "\tint indented;", Kind: "v"},
		},
		{
			name: "escaped delimiter in pattern",
			line: `path	f.c	/^char \/tmp\/x$/;"	v`,
			want: Entry{Name: "path", File: "f.c", Pattern: "char /tmp/x", Kind: "v"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseEntry(tt.line)
			if !ok {
				t.Fatalf("parseEntry(%q) reported failure", tt.line)
			}
			if got != tt.want {
				t.Errorf("parseEntry(%q):\n got %+v\nwant %+v", tt.line, got, tt.want)
			}
		})
	}
}

func TestParseEntryRejects(t *testing.T) {
	for _, line := range []string{
		"",
		"!_TAG_FILE_SORTED\t1\t/0=unsorted/",
		"nofields",
		"name\tonlyfile",
	} {
		if _, ok := parseEntry(line); ok {
			t.Errorf("parseEntry(%q) should have failed", line)
		}
	}
}

// writeTags builds a tags file with the given entry lines, sorted, with the
// header ctags would emit.
func writeTags(t *testing.T, dir string, sorted string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, "tags")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	fmt.Fprintf(w, "!_TAG_FILE_FORMAT\t2\t/extended format/\n")
	fmt.Fprintf(w, "!_TAG_FILE_SORTED\t%s\t/0=unsorted, 1=sorted, 2=foldcase/\n", sorted)
	fmt.Fprintf(w, "!_TAG_PROGRAM_NAME\tUniversal Ctags\t//\n")
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLookupSmallFileUsesMemory(t *testing.T) {
	path := writeTags(t, t.TempDir(), "1", []string{
		"alpha\ta.c\t/^alpha$/;\"\tf",
		"beta\tb.c\t/^beta$/;\"\tv",
		"beta\tc.c\t/^beta$/;\"\tf",
		"gamma\td.c\t/^gamma$/;\"\tf",
	})
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if !r.InMemory() {
		t.Error("a tiny tags file should be loaded into memory")
	}
	got, err := r.Lookup("beta")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Lookup(beta) returned %d entries, want 2", len(got))
	}
	if got, err := r.Lookup("missing"); err != nil || len(got) != 0 {
		t.Errorf("Lookup(missing) = %v, %v; want empty", got, err)
	}
}

// synthTags writes a tags file large enough to force on-disk binary search,
// and returns the names it contains.
func synthTags(t *testing.T, n int) (path string, names []string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "tags")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	fmt.Fprintf(w, "!_TAG_FILE_FORMAT\t2\t/extended format/\n")
	fmt.Fprintf(w, "!_TAG_FILE_SORTED\t1\t/0=unsorted, 1=sorted, 2=foldcase/\n")

	names = make([]string, 0, n)
	for i := 0; i < n; i++ {
		names = append(names, fmt.Sprintf("sym_%07d", i))
	}
	sort.Strings(names)
	for i, name := range names {
		// Vary line length so the binary search cannot rely on fixed records.
		pad := strings.Repeat("x", i%40)
		fmt.Fprintf(w, "%s\tsrc/file%03d.c\t/^int %s(void) %s$/;\"\tf\n", name, i%500, name, pad)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	return path, names
}

// The load-bearing test: on a file too large to hold in memory, binary search
// must return exactly what a linear scan would, for every name and for names
// that are absent.
func TestBinarySearchMatchesLinearScan(t *testing.T) {
	const n = 200_000
	path, names := synthTags(t, n)

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() <= memoryLimit {
		t.Fatalf("synthetic file is %d bytes, not above the %d limit; "+
			"this test would not exercise the on-disk path", fi.Size(), memoryLimit)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.InMemory() {
		t.Fatal("large file was loaded into memory; the on-disk path is untested")
	}
	if !r.Sorted() {
		t.Fatal("file should be recognized as sorted")
	}

	// Probe the boundaries and a spread through the middle.
	probes := []string{names[0], names[1], names[n/2], names[n-2], names[n-1]}
	for i := 0; i < n; i += 4093 { // a prime stride, to avoid alignment luck
		probes = append(probes, names[i])
	}
	for _, name := range probes {
		got, err := r.Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%s): %v", name, err)
		}
		if len(got) != 1 {
			t.Fatalf("Lookup(%s) returned %d entries, want 1", name, len(got))
		}
		if got[0].Name != name {
			t.Fatalf("Lookup(%s) returned %q", name, got[0].Name)
		}
		wantPattern := "int " + name + "(void)"
		if !strings.HasPrefix(got[0].Pattern, wantPattern) {
			t.Fatalf("Lookup(%s) pattern = %q, want prefix %q", name, got[0].Pattern, wantPattern)
		}
	}

	// Absent names must return nothing, including names that sort before the
	// first entry and after the last.
	for _, absent := range []string{"aaaa_before_everything", "zzzz_after_everything",
		"sym_0000000x", "sym_9999999"} {
		got, err := r.Lookup(absent)
		if err != nil {
			t.Fatalf("Lookup(%s): %v", absent, err)
		}
		if len(got) != 0 {
			t.Errorf("Lookup(%s) returned %d entries, want none", absent, len(got))
		}
	}
}

// The whole point of the on-disk path is that a huge tags file costs almost no
// memory. If this regresses, the editor becomes unusable on a big codebase.
func TestLookupDoesNotLoadTheFile(t *testing.T) {
	path, names := synthTags(t, 200_000)
	fi, _ := os.Stat(path)

	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := 0; i < 500; i++ {
		if _, err := r.Lookup(names[(i*397)%len(names)]); err != nil {
			t.Fatal(err)
		}
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if growth < 0 {
		growth = 0
	}
	// Generous bound: the point is that it is nowhere near the file size.
	if limit := fi.Size() / 8; growth > limit {
		t.Errorf("500 lookups grew the heap by %d bytes on a %d byte file; "+
			"it looks like the file is being read into memory", growth, fi.Size())
	}
}

func TestPrefixSearch(t *testing.T) {
	path := writeTags(t, t.TempDir(), "1", []string{
		"read\ta.c\t/^read$/;\"\tf",
		"read_buf\tb.c\t/^read_buf$/;\"\tf",
		"read_file\tc.c\t/^read_file$/;\"\tf",
		"reader\td.c\t/^reader$/;\"\tv",
		"write\te.c\t/^write$/;\"\tf",
	})
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	got, err := r.Prefix("read", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Errorf("Prefix(read) returned %d entries, want 4", len(got))
	}
	for _, e := range got {
		if !strings.HasPrefix(e.Name, "read") {
			t.Errorf("Prefix(read) returned %q", e.Name)
		}
	}

	if got, _ := r.Prefix("read", 2); len(got) != 2 {
		t.Errorf("limit not honored: got %d entries, want 2", len(got))
	}
}

func TestPrefixSearchOnDisk(t *testing.T) {
	path, _ := synthTags(t, 200_000)
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.InMemory() {
		t.Fatal("expected the on-disk path")
	}

	got, err := r.Prefix("sym_000001", 100)
	if err != nil {
		t.Fatal(err)
	}
	// sym_0000010 through sym_0000019 all share this prefix.
	if len(got) != 10 {
		t.Errorf("Prefix returned %d entries, want 10", len(got))
	}
	for _, e := range got {
		if !strings.HasPrefix(e.Name, "sym_000001") {
			t.Errorf("Prefix returned non-matching %q", e.Name)
		}
	}
}

// A foldcase-sorted file groups names that differ only in case, so an exact
// lookup has to filter within the run rather than trusting the ordering.
func TestFoldCaseSorting(t *testing.T) {
	path := writeTags(t, t.TempDir(), "2", []string{
		"Apple\ta.c\t/^Apple$/;\"\tv",
		"apple\tb.c\t/^apple$/;\"\tf",
		"APPLE\tc.c\t/^APPLE$/;\"\td",
		"banana\td.c\t/^banana$/;\"\tf",
	})
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	got, err := r.Lookup("apple")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].File != "b.c" {
		t.Errorf("Lookup(apple) = %+v, want exactly the lowercase entry from b.c", got)
	}
	if got, _ := r.Lookup("APPLE"); len(got) != 1 || got[0].File != "c.c" {
		t.Errorf("Lookup(APPLE) = %+v, want the entry from c.c", got)
	}
}

func TestUnsortedSmallFileStillWorks(t *testing.T) {
	path := writeTags(t, t.TempDir(), "0", []string{
		"zeta\tz.c\t/^zeta$/;\"\tf",
		"alpha\ta.c\t/^alpha$/;\"\tf",
		"mid\tm.c\t/^mid$/;\"\tf",
	})
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if !r.InMemory() {
		t.Error("an unsorted file must be indexed in memory")
	}
	if got, _ := r.Lookup("alpha"); len(got) != 1 {
		t.Error("lookup failed on an unsorted file")
	}
}

func TestResolveRelativePaths(t *testing.T) {
	dir := t.TempDir()
	path := writeTags(t, dir, "1", []string{"x\tsrc/x.c\t1;\"\tf"})
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	got, _ := r.Lookup("x")
	if len(got) != 1 {
		t.Fatal("lookup failed")
	}
	want := filepath.Join(dir, "src/x.c")
	if resolved := r.Resolve(got[0]); resolved != want {
		t.Errorf("Resolve = %q, want %q", resolved, want)
	}

	abs := Entry{File: "/absolute/path.c"}
	if resolved := r.Resolve(abs); resolved != "/absolute/path.c" {
		t.Errorf("absolute path was rewritten to %q", resolved)
	}
}

func TestOpenRejectsMissingFile(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("Open should fail on a missing file")
	}
}

func TestEmptyAndHeaderOnlyFiles(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "tags")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Open(empty)
	if err != nil {
		t.Fatalf("an empty tags file should open cleanly: %v", err)
	}
	if got, err := r.Lookup("anything"); err != nil || len(got) != 0 {
		t.Errorf("Lookup on empty file = %v, %v", got, err)
	}
	r.Close()

	headerOnly := writeTags(t, t.TempDir(), "1", nil)
	r2, err := Open(headerOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if got, err := r2.Lookup("anything"); err != nil || len(got) != 0 {
		t.Errorf("Lookup on header-only file = %v, %v", got, err)
	}
}

func TestClosedReaderIsSafe(t *testing.T) {
	path := writeTags(t, t.TempDir(), "1", []string{"a\ta.c\t1;\"\tf"})
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if _, err := r.Lookup("a"); err == nil {
		t.Error("Lookup after Close should report an error, not crash")
	}
	if err := r.Close(); err != nil {
		t.Errorf("double Close returned %v", err)
	}
}

func BenchmarkLookupOnDisk(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "tags")
	f, _ := os.Create(path)
	w := bufio.NewWriter(f)
	fmt.Fprintf(w, "!_TAG_FILE_SORTED\t1\t//\n")
	const n = 300_000
	for i := 0; i < n; i++ {
		fmt.Fprintf(w, "sym_%07d\tsrc/f%03d.c\t/^int sym_%07d(void)$/;\"\tf\n", i, i%500, i)
	}
	w.Flush()
	f.Close()

	r, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	if r.InMemory() {
		b.Fatal("expected the on-disk path")
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		if _, err := r.Lookup(fmt.Sprintf("sym_%07d", (i*397)%n)); err != nil {
			b.Fatal(err)
		}
		i++
	}
}
