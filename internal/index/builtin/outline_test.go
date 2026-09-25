package builtin

import (
	"fmt"
	"strings"
	"testing"
)

func outlineOf(path, src string) []string {
	lines := strings.Split(src, "\n")
	var out []string
	for _, s := range Outline(path, len(lines), func(i int) []byte { return []byte(lines[i]) }) {
		out = append(out, fmt.Sprintf("%s:%s:%d", s.Name, s.Kind, s.Loc.Line))
	}
	return out
}

func TestOutlineOfACFile(t *testing.T) {
	src := `#include <linux/fs.h>
#define MAX_ITEMS 16

struct item {
	int id;
};

enum state { IDLE, BUSY };

static int count_items(struct item *list, int n)
{
	struct item *it;
	struct item copy = *list;
	return n;
}

int main(void)
{
	return count_items(0, 0);
}`
	got := strings.Join(outlineOf("/p/a.c", src), " ")
	want := "MAX_ITEMS:macro:2 item:struct:4 state:enum:8 count_items:func:10 main:func:17"
	if got != want {
		t.Errorf("outline\n got: %s\nwant: %s", got, want)
	}
}

func TestOutlineOfAGoFile(t *testing.T) {
	src := "package x\n\ntype T struct {\n\tA int\n}\n\nfunc (t *T) M() {}\n\nfunc F() {}\n"
	got := strings.Join(outlineOf("/p/x.go", src), " ")
	for _, want := range []string{"T:", "M:", "F:"} {
		if !strings.Contains(got, want) {
			t.Errorf("outline %q lacks %s", got, want)
		}
	}
}

func TestOutlineOfAnUnknownLanguageIsEmpty(t *testing.T) {
	if got := outlineOf("/p/notes.txt", "func F() {}\n"); len(got) != 0 {
		t.Errorf("outline of a text file = %v", got)
	}
}
