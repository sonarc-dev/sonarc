package buffer

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// The buffer is checked against the plainest possible model — a slice of
// strings — through thousands of random edits on a file far larger than the
// gap. The gap buffer once cleared lines it had just moved whenever the gap
// travelled back further than its own size: the first Enter typed near the
// top of any real file blanked everything below. Small test files never moved
// the gap that far, so nothing noticed.
func TestBufferMatchesAModelUnderRandomEdits(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		r := rand.New(rand.NewSource(seed))
		var model []string
		for i := 0; i < 2000; i++ {
			model = append(model, fmt.Sprintf("line %d %s", i, strings.Repeat("x", r.Intn(20))))
		}
		b := FromBytes([]byte(strings.Join(model, "\n") + "\n"))

		for step := 0; step < 300; step++ {
			line := r.Intn(len(model))
			col := r.Intn(len(model[line]) + 1)
			switch r.Intn(3) {
			case 0: // insert text, sometimes spanning lines
				s := "ins"
				for k := r.Intn(4); k > 0; k-- {
					s += "\nnew" + fmt.Sprint(step)
				}
				b.insert(Pos{line, col}, []byte(s))
				parts := strings.Split(s, "\n")
				head, tail := model[line][:col], model[line][col:]
				repl := append([]string{head + parts[0]}, parts[1:]...)
				repl[len(repl)-1] += tail
				model = append(model[:line], append(repl, model[line+1:]...)...)
			case 1: // delete, sometimes across lines
				end := min(line+r.Intn(5), len(model)-1)
				endCol := r.Intn(len(model[end]) + 1)
				if end == line && endCol < col {
					col, endCol = endCol, col
				}
				b.remove(Pos{line, col}, Pos{end, endCol})
				joined := model[line][:col] + model[end][endCol:]
				model = append(model[:line], append([]string{joined}, model[end+1:]...)...)
			case 2: // a single newline, the edit that exposed the bug
				b.insert(Pos{line, col}, []byte("\n"))
				head, tail := model[line][:col], model[line][col:]
				model = append(model[:line], append([]string{head, tail}, model[line+1:]...)...)
			}
			if b.NumLines() != len(model) {
				t.Fatalf("seed %d step %d: %d lines, model has %d", seed, step, b.NumLines(), len(model))
			}
			for i := range model {
				if got := string(b.Line(i)); got != model[i] {
					t.Fatalf("seed %d step %d: line %d = %q, want %q", seed, step, i, got, model[i])
				}
			}
		}
	}
}
