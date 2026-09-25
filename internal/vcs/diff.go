// Package vcs reads what git knows about a project: which files have changed,
// the committed version of a file, and how the text in the editor differs
// from it. Everything runs git as a subprocess; nothing here writes to the
// repository.
package vcs

import (
	"bytes"
	"hash/maphash"
)

// Hunk is one run of changed lines. Starts are 0-based line indexes; a hunk
// with no old lines is a pure insertion, one with no new lines a pure deletion
// just before NewStart.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
}

// maxEditCost bounds the work spent finding a minimal diff. Past it, the
// differing middle (after the common start and end are trimmed) is reported
// as one hunk: correct, if coarser, and never slow. Real edits in an editor
// are far below it; it only matters when a file was rewritten wholesale.
//
// The search keeps its frontier for every step to recover the path, which is
// quadratic in the cost; at this bound that is a few megabytes at worst.
const maxEditCost = 1000

// Lines compares two texts line by line and returns the changed runs, in
// order. Its result is a minimal diff whenever the edit is within maxEditCost.
func Lines(old, new [][]byte) []Hunk {
	// Common prefix and suffix cost nothing to find and are almost the whole
	// file for typical edits, so the search below works on the middle only.
	pre := 0
	for pre < len(old) && pre < len(new) && bytes.Equal(old[pre], new[pre]) {
		pre++
	}
	suf := 0
	for suf < len(old)-pre && suf < len(new)-pre &&
		bytes.Equal(old[len(old)-1-suf], new[len(new)-1-suf]) {
		suf++
	}
	a, b := old[pre:len(old)-suf], new[pre:len(new)-suf]
	if len(a) == 0 && len(b) == 0 {
		return nil
	}

	ha, hb := hashLines(a, b)
	pairs, ok := myers(ha, hb, a, b)
	if !ok {
		return []Hunk{{OldStart: pre, OldLines: len(a), NewStart: pre, NewLines: len(b)}}
	}

	// Walk the matched pairs; the gaps between them are the hunks.
	var out []Hunk
	i, j := 0, 0
	for _, p := range append(pairs, [2]int{len(a), len(b)}) {
		if p[0] > i || p[1] > j {
			out = append(out, Hunk{OldStart: pre + i, OldLines: p[0] - i, NewStart: pre + j, NewLines: p[1] - j})
		}
		i, j = p[0]+1, p[1]+1
	}
	return out
}

var seed = maphash.MakeSeed()

// hashLines reduces lines to numbers so comparisons in the search are cheap.
// A collision is resolved by comparing the lines themselves.
func hashLines(a, b [][]byte) ([]uint64, []uint64) {
	h := func(ls [][]byte) []uint64 {
		out := make([]uint64, len(ls))
		for i, l := range ls {
			out[i] = maphash.Bytes(seed, l)
		}
		return out
	}
	return h(a), h(b)
}

// myers finds a shortest edit script between a and b (Myers 1986) and returns
// the lines they share as (index in a, index in b) pairs in order. It gives up
// once the script would cost more than maxEditCost.
func myers(ha, hb []uint64, a, b [][]byte) ([][2]int, bool) {
	n, m := len(a), len(b)
	eq := func(x, y int) bool { return ha[x] == hb[y] && bytes.Equal(a[x], b[y]) }
	maxD := min(n+m, maxEditCost)
	off := maxD + 1
	v := make([]int, 2*maxD+3)
	var trace [][]int

	for d := 0; d <= maxD; d++ {
		// Step d reads only diagonals -d-1..d+1 of the previous frontier.
		trace = append(trace, append([]int(nil), v[off-d-1:off+d+2]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // step down: insertion from b
			} else {
				x = v[off+k-1] + 1 // step right: deletion from a
			}
			y := x - k
			for x < n && y < m && eq(x, y) {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, n, m, eq), true
			}
		}
	}
	return nil, false
}

// backtrack recovers the matched lines from the saved search frontiers.
func backtrack(trace [][]int, n, m int, eq func(int, int) bool) [][2]int {
	var pairs [][2]int
	x, y := n, m
	for d := len(trace) - 1; d >= 0 && (x > 0 || y > 0); d-- {
		snap := trace[d]
		at := func(k int) int { return snap[k+d+1] } // snap starts at diagonal -d-1
		k := x - y
		var pk int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := at(pk)
		py := px - pk
		for x > px && y > py {
			x, y = x-1, y-1
			pairs = append(pairs, [2]int{x, y})
		}
		if d > 0 {
			x, y = px, py
		}
	}
	// Any diagonal left at the very start was matched before the first edit.
	for x > 0 && y > 0 && eq(x-1, y-1) {
		x, y = x-1, y-1
		pairs = append(pairs, [2]int{x, y})
	}
	for i, j := 0, len(pairs)-1; i < j; i, j = i+1, j-1 {
		pairs[i], pairs[j] = pairs[j], pairs[i]
	}
	return pairs
}
