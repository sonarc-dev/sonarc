package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/view"
)

// session is how a project was left: the files open in it, where each one's
// cursor was, which was showing, and which folders of the tree were open.
// Opening the project again with no file named picks up from there.
type session struct {
	Files  []spot   `json:"files"`
	Active int      `json:"active"`
	Dirs   []string `json:"open_dirs,omitempty"`
}

// spot is a position in a file. Line and Col are 0-based buffer positions.
type spot struct {
	Path string `json:"path,omitempty"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Top  int    `json:"top"`
	Used int64  `json:"used,omitempty"` // unix seconds, to forget the oldest
}

// maxPlaces bounds how many files' last positions are remembered.
const maxPlaces = 1000

// stateDir is where state.json and the per-project files live; empty when
// nothing is kept, as in tests.
func (a *app) stateDir() string {
	if a.statePath == "" {
		return ""
	}
	return filepath.Dir(a.statePath)
}

// sessionPath is this project's session file, named by a hash of its root so
// any path can be stored under a fixed-length name.
func (a *app) sessionPath() string {
	dir := a.stateDir()
	if dir == "" || a.root == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(a.root))
	return filepath.Join(dir, "sessions", hex.EncodeToString(sum[:8])+".json")
}

func (a *app) placesPath() string {
	if dir := a.stateDir(); dir != "" {
		return filepath.Join(dir, "positions.json")
	}
	return ""
}

// placeOf is where v's cursor and scroll are now.
func placeOf(v *view.View) spot {
	return spot{Path: v.Buf.Path(), Line: v.Head.Line, Col: v.Head.Col, Top: v.Top}
}

// restorePlace puts v's cursor back where p says, as far as the file still
// reaches.
func restorePlace(v *view.View, p spot) {
	v.SetCursor(v.Buf.Clamp(buffer.Pos{Line: p.Line, Col: p.Col}))
	v.Top = min(max(p.Top, 0), max(v.Buf.NumLines()-1, 0))
}

// restoreSession reopens the project's files as they were left. It is used
// only when the command line named no file: naming one says what to work on.
func (a *app) restoreSession() {
	var s session
	if !readJSON(a.sessionPath(), &s) || len(s.Files) == 0 {
		return
	}
	var views []*view.View
	active := 0
	for i, f := range s.Files {
		if _, err := os.Stat(f.Path); err != nil {
			continue // deleted or moved since; nothing to reopen
		}
		b, err := buffer.Open(f.Path)
		if err != nil {
			continue
		}
		v := view.New(b)
		restorePlace(v, f)
		if i == s.Active {
			active = len(views)
		}
		views = append(views, v)
	}
	if t := a.ui.Sidebar.Tree; t != nil {
		for _, d := range s.Dirs {
			t.Expand(d)
		}
	}
	if len(views) == 0 {
		return
	}
	// The empty buffer a folder opens with has nothing in it to keep.
	a.views = views
	a.switchTo(active)
	a.ui.Sidebar.Focused = false
}

// saveSession records the project's open files, cursor positions and open
// folders. It is called every few seconds as well as on exit, since an ssh
// connection that drops takes the editor with it; the file is only written
// when something in it has changed.
func (a *app) saveSession() {
	path := a.sessionPath()
	if path == "" {
		return
	}
	s := session{Active: 0}
	for _, v := range a.views {
		if v.Buf.Path() == "" {
			continue
		}
		if v.Buf == a.v().Buf {
			s.Active = len(s.Files)
		}
		s.Files = append(s.Files, placeOf(v))
	}
	if t := a.ui.Sidebar.Tree; t != nil {
		s.Dirs = t.Expanded()
	}
	data, err := json.Marshal(s)
	if err != nil || string(data) == a.savedSession {
		return
	}
	if writeFile(path, data) {
		a.savedSession = string(data)
	}
}

// rememberPlaces records where each view's cursor is, so reopening the file
// later returns there.
func (a *app) rememberPlaces(vs ...*view.View) {
	path := a.placesPath()
	if path == "" {
		return
	}
	places := map[string]spot{}
	readJSON(path, &places)
	now := a.now().Unix()
	for _, v := range vs {
		if v.Buf.Path() == "" {
			continue
		}
		p := placeOf(v)
		p.Path, p.Used = "", now
		places[v.Buf.Path()] = p
	}
	if len(places) > maxPlaces {
		keys := make([]string, 0, len(places))
		for k := range places {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return places[keys[i]].Used < places[keys[j]].Used })
		for _, k := range keys[:len(places)-maxPlaces] {
			delete(places, k)
		}
	}
	writeJSON(path, places)
}

// recallPlace puts a newly opened file's cursor where it was last left.
func (a *app) recallPlace(v *view.View) {
	path := a.placesPath()
	if path == "" || v.Buf.Path() == "" {
		return
	}
	places := map[string]spot{}
	if readJSON(path, &places) {
		if p, ok := places[v.Buf.Path()]; ok {
			restorePlace(v, p)
		}
	}
}

// readJSON loads path into v, reporting whether it could. Like state.json,
// a missing or damaged file just means starting fresh.
func readJSON(path string, v any) bool {
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, v) == nil
}

// writeJSON saves v to path atomically, quietly giving up on failure.
func writeJSON(path string, v any) {
	if data, err := json.Marshal(v); err == nil {
		writeFile(path, data)
	}
}

// writeFile writes data through a temporary file and a rename, so two
// editors saving at once, or a crash mid-write, cannot leave a torn file. It
// reports success; failure is otherwise silent, since none of this state is
// worth interrupting the user over.
func writeFile(path string, data []byte) bool {
	dir := filepath.Dir(path)
	if os.MkdirAll(dir, 0o755) != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".tmp-*.json")
	if err != nil {
		return false
	}
	_, werr := f.Write(append(data, '\n'))
	if cerr := f.Close(); werr != nil || cerr != nil {
		_ = os.Remove(f.Name())
		return false
	}
	if os.Rename(f.Name(), path) != nil {
		_ = os.Remove(f.Name())
		return false
	}
	return true
}
