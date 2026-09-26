package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/sonarc-dev/sonarc/internal/term"
	"github.com/sonarc-dev/sonarc/internal/ui"
)

// state is what sonarc remembers between sessions. It is deliberately tiny:
// layout the user chose by hand, never anything about a project.
type state struct {
	SidebarWidth int    `json:"sidebar_width,omitempty"`
	Theme        string `json:"theme,omitempty"`
	// ChangesCollapsed keeps the sidebar's Changes section closed.
	ChangesCollapsed bool `json:"changes_collapsed,omitempty"`
}

// defaultStatePath is ~/.config/sonarc/state.json on Linux, or the platform's
// equivalent; empty if there is no home to keep it in.
func defaultStatePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	migrateConfig(dir)
	return filepath.Join(dir, "sonarc", "state.json")
}

// migrateConfig moves state kept under the editor's former name, keditor, to
// its current place, once: theme, sidebar width and every project's session
// carry over. It does nothing when the new directory already exists.
func migrateConfig(dir string) {
	now := filepath.Join(dir, "sonarc")
	if _, err := os.Stat(now); err == nil {
		return
	}
	_ = os.Rename(filepath.Join(dir, "keditor"), now)
}

// loadState applies the saved state. A missing or unreadable file means the
// defaults, never an error: none of this is worth refusing to start over.
func (a *app) loadState() {
	if a.statePath == "" {
		return
	}
	data, err := os.ReadFile(a.statePath)
	if err != nil {
		return
	}
	var st state
	if json.Unmarshal(data, &st) != nil {
		return
	}
	if st.SidebarWidth > 0 {
		a.ui.Sidebar.Width = max(st.SidebarWidth, ui.SidebarMinWidth)
	}
	a.ui.Sidebar.Changes.Collapsed = st.ChangesCollapsed
	if st.Theme != "" {
		a.setTheme(st.Theme) // an unknown name keeps the default
	}
}

// saveState writes the state; failure is silent for the same reason loading
// is forgiving.
func (a *app) saveState() {
	if a.statePath == "" {
		return
	}
	writeJSON(a.statePath, state{
		SidebarWidth:     a.ui.SidebarWidth(),
		Theme:            a.themeName,
		ChangesCollapsed: a.ui.Sidebar.Changes.Collapsed,
	})
}

// setTheme switches to the named theme, reporting whether it exists.
func (a *app) setTheme(name string) bool {
	t, ok := term.NamedTheme(name, a.scr.Caps)
	if !ok {
		return false
	}
	a.scr.Theme = t
	a.themeName = name
	a.scr.SetStyle(t.Text)
	return true
}

// cmdSelectTheme picks a color theme; the choice is kept for next time.
func (a *app) cmdSelectTheme() {
	current := a.themeName
	if current == "" {
		current = term.DefaultThemeName
	}
	var items []ui.PickerItem
	sel := 0
	for i, n := range term.ThemeNames() {
		detail := ""
		if n == current {
			detail, sel = "(current)", i
		}
		items = append(items, ui.PickerItem{Label: n, Detail: detail})
	}
	a.ui.ShowPicker("color theme", items)
	a.ui.Picker.Move(sel)
	a.pickAction = func(it ui.PickerItem) {
		a.setTheme(it.Label)
		a.saveState()
		if a.scr.Caps.Colors < 256 {
			a.ui.Notify("theme %s saved; this terminal shows %d colors, so the basic palette stays in use", it.Label, a.scr.Caps.Colors)
			return
		}
		a.ui.Notify("theme: %s", it.Label)
	}
}
