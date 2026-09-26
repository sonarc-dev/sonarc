package rebuild

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sonarc-dev/sonarc/internal/index/cscope"
	"github.com/sonarc-dev/sonarc/internal/index/tags"
)

// The generic path against the real cscope and ctags. Skipped where they are not
// installed (macOS ships neither usable); it runs on the Linux server.
func TestGenericRebuildWithRealTools(t *testing.T) {
	if _, err := exec.LookPath("cscope"); err != nil {
		t.Skip("cscope is not installed")
	}
	if _, _, ok := tags.DetectCtags(); !ok {
		t.Skip("no usable ctags")
	}
	root := t.TempDir()
	src := "int helper(void)\n{\n\treturn 1;\n}\n\nint main(void)\n{\n\treturn helper();\n}\n"
	write(t, filepath.Join(root, "a.c"), src)
	write(t, filepath.Join(root, "dir with space", "b.c"), "int spaced(void) { return 2; }\n")
	write(t, filepath.Join(root, "tags"), "OLD TAGS") // replaced atomically

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := Detect(root)
	if p.Kernel || p.ListFrom != "project file list" {
		t.Fatalf("plan = %+v", p)
	}
	for _, s := range Run(ctx, p, nil) {
		if s.Err != nil || s.Skipped != "" {
			t.Fatalf("step %s: %+v", s.Name, s)
		}
	}

	db, err := cscope.Open(filepath.Join(root, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if res, err := db.Query(ctx, cscope.FindDefinition, "helper"); err != nil || len(res) == 0 {
		t.Errorf("cscope definition of helper: %v %v", res, err)
	}
	// cscope cannot store a path with a space; such a file must be left out of
	// its database rather than put in and break queries.
	if res, err := db.Query(ctx, cscope.FindDefinition, "spaced"); err != nil || len(res) != 0 {
		t.Errorf("cscope definition of spaced: %v %v, want no entry and no error", res, err)
	}
	for _, n := range []string{"cscope.out.in", "cscope.out.po"} {
		if !exists(filepath.Join(root, n)) {
			t.Errorf("%s missing: the -q inverted index was not installed where cscope -f cscope.out reads it", n)
		}
	}

	tf := read(t, filepath.Join(root, "tags"))
	if !strings.Contains(tf, "!_TAG_FILE_SORTED\t1") {
		t.Error("tags file is not sorted; on-disk binary search needs it")
	}
	// ctags does handle the spaced path, so tags covers it.
	for _, sym := range []string{"helper\t", "spaced\t"} {
		if !strings.Contains(tf, "\n"+sym) {
			t.Errorf("tags has no entry for %q", strings.TrimSpace(sym))
		}
	}
	r, err := tags.Open(filepath.Join(root, "tags"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	assertNoLeftovers(t, root)
}
