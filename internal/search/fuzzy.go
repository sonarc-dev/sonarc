package search

import "strings"

// maxStarts bounds how many starting positions are tried when scoring. Beyond a
// handful, later starts are worse anyway, and the picker scores thousands of
// candidates on every keystroke.
const maxStarts = 16

// FuzzyScore rates how well pattern matches candidate, returning false when the
// pattern's characters do not all appear in order.
//
// Scoring is tuned for paths and command names: consecutive matches and matches
// at word or path boundaries score highest, so typing "ifr" finds
// "internal/index/find_references.go" ahead of things that merely contain those
// letters scattered about. Matching is case-insensitive unless the pattern
// contains a capital.
//
// A purely greedy scan takes the first occurrence of each character, which
// picks badly: for "buf", the first 'b' in "lib/buffer.go" is the one in "lib",
// so the strong match at the path segment is never scored. Several starting
// positions are therefore tried and the best kept.
func FuzzyScore(candidate, pattern string) (int, bool) {
	if pattern == "" {
		return 0, true
	}
	if len(pattern) > len(candidate) {
		return 0, false
	}

	hay := candidate
	if strings.ToLower(pattern) == pattern {
		hay = strings.ToLower(candidate)
	}

	best := 0
	found := false
	starts := 0
	for i := 0; i+len(pattern) <= len(hay) && starts < maxStarts; i++ {
		if hay[i] != pattern[0] {
			continue
		}
		starts++
		if s, ok := scoreFrom(candidate, hay, pattern, i); ok {
			if !found || s > best {
				best, found = s, true
			}
		}
	}
	if !found {
		return 0, false
	}
	// Prefer shorter candidates: a near-exact match should outrank a long path
	// that merely contains the same letters.
	return best - len(candidate)/16, true
}

// scoreFrom greedily matches pattern into hay beginning at start, which must
// already match pattern[0].
func scoreFrom(candidate, hay, pattern string, start int) (int, bool) {
	score := 0
	ci := start
	prevMatch := -2

	for pi := 0; pi < len(pattern); pi++ {
		want := pattern[pi]
		at := -1
		for ; ci < len(hay); ci++ {
			if hay[ci] == want {
				at = ci
				break
			}
		}
		if at < 0 {
			return 0, false
		}

		// Consecutive characters are a far stronger signal than scattered ones:
		// "read" in "read_write.c" should beat "r..e..a..d" elsewhere.
		if at == prevMatch+1 {
			score += 15
		}
		switch {
		case at == 0:
			score += 20
		case isBoundary(candidate[at-1]):
			score += 12
		case isLower(candidate[at-1]) && isUpper(candidate[at]):
			score += 10 // camelCase boundary
		}
		// Earlier matches are slightly better than later ones.
		score -= at / 8

		prevMatch = at
		ci++
	}
	return score, true
}

func isBoundary(b byte) bool {
	return b == '/' || b == '_' || b == '-' || b == '.' || b == ' ' || b == '\\'
}

func isLower(b byte) bool { return b >= 'a' && b <= 'z' }
func isUpper(b byte) bool { return b >= 'A' && b <= 'Z' }
