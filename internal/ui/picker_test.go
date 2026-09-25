package ui

import (
	"fmt"
	"testing"
)

// On a kernel checkout the alphabetically-first 5000 paths never reach fs/, so
// truncating the candidate list before filtering left fs/namei.c unreachable no
// matter what was typed. Ranking has to see every candidate; only the ranked
// output is capped. Found by running against a real kernel tree.
func TestPickerRanksAcrossAllCandidatesNotJustAPrefix(t *testing.T) {
	var items []PickerItem
	// Distractors that genuinely fuzzy-match "namei" (n-vme, a-pi, m-odel,
	// e, i-ndex) but only as scattered letters.
	for i := 0; i < 12000; i++ {
		items = append(items, PickerItem{
			Label:  fmt.Sprintf("index%05d.yaml", i),
			Detail: fmt.Sprintf("Documentation/nvme/api/model/index%05d.yaml", i),
		})
	}
	// The file actually wanted, far past any prefix cap.
	items = append(items, PickerItem{Label: "namei.c", Detail: "fs/namei.c"})

	var u UI
	u.ShowPicker("open file", items)
	u.Picker.SetFilter("namei")

	if u.Picker.Count() == 0 {
		t.Fatal("no matches: a file past the prefix cap was unreachable")
	}
	got, ok := u.Picker.Current()
	if !ok {
		t.Fatal("picker reported matches but had no current item")
	}
	if got.Detail != "fs/namei.c" {
		t.Errorf("top match = %q, want %q (consecutive match at a path boundary must win)", got.Detail, "fs/namei.c")
	}
}

// Ranking every candidate must not mean retaining every candidate: the picker
// shows a screenful, so the kept set stays bounded on a huge tree.
func TestPickerBoundsRetainedMatches(t *testing.T) {
	var items []PickerItem
	for i := 0; i < 20000; i++ {
		items = append(items, PickerItem{
			Label:  fmt.Sprintf("alpha%05d.go", i),
			Detail: fmt.Sprintf("src/alpha%05d.go", i),
		})
	}
	var u UI
	u.ShowPicker("open file", items)
	u.Picker.SetFilter("alpha")

	if n := u.Picker.Count(); n == 0 || n > maxRankedMatches {
		t.Errorf("Count() = %d, want between 1 and %d", n, maxRankedMatches)
	}
}
