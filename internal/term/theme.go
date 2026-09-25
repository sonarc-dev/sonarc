package term

import "github.com/gdamore/tcell/v2"

// Theme is the editor's palette.
//
// Colors are given as 256-color palette indices rather than RGB. That is
// deliberate: the primary environment is ssh into tmux, where 256 colors is
// what actually arrives and truecolor usually does not. tcell maps palette
// indices up to RGB on terminals that support it, so this looks correct
// everywhere instead of being dithered down from a design nobody sees.
type Theme struct {
	Text          tcell.Style
	CursorLine    tcell.Style
	Selection     tcell.Style
	Match         tcell.Style
	Gutter        tcell.Style
	GutterCurrent tcell.Style
	Status        tcell.Style
	StatusMod     tcell.Style
	StatusDim     tcell.Style
	Message       tcell.Style
	Error         tcell.Style

	// Changes against git's last commit: gutter marks and file-tree badges.
	GitAdded    tcell.Style
	GitModified tcell.Style
	GitDeleted  tcell.Style

	// Syntax classes.
	Keyword tcell.Style
	String  tcell.Style
	Comment tcell.Style
	Number  tcell.Style
	Type    tcell.Style
	Func    tcell.Style
	Preproc tcell.Style
}

func pal(n int32) tcell.Color { return tcell.PaletteColor(int(n)) }

// DefaultTheme is a dark theme tuned for 256-color terminals.
//
// Body text uses the terminal's own default foreground and background rather
// than painting its own. That keeps the editor consistent with the user's
// terminal theme and transparency instead of stamping a rectangle of slightly
// wrong grey over it.
func DefaultTheme() Theme {
	base := tcell.StyleDefault
	return Theme{
		Text:          base,
		CursorLine:    base.Background(pal(236)),
		Selection:     base.Background(pal(24)),
		Match:         base.Background(pal(58)).Foreground(pal(230)),
		Gutter:        base.Foreground(pal(242)),
		GutterCurrent: base.Foreground(pal(252)),
		Status:        base.Background(pal(238)).Foreground(pal(252)),
		StatusMod:     base.Background(pal(238)).Foreground(pal(179)).Bold(true),
		StatusDim:     base.Background(pal(238)).Foreground(pal(245)),
		Message:       base.Foreground(pal(252)),
		Error:         base.Foreground(pal(203)).Bold(true),

		Keyword: base.Foreground(pal(75)),
		String:  base.Foreground(pal(173)),
		Comment: base.Foreground(pal(65)),
		Number:  base.Foreground(pal(151)),
		Type:    base.Foreground(pal(79)),
		Func:    base.Foreground(pal(187)),
		Preproc: base.Foreground(pal(176)),

		GitAdded:    base.Foreground(pal(71)),
		GitModified: base.Foreground(pal(68)),
		GitDeleted:  base.Foreground(pal(167)),
	}
}

// MinimalTheme uses only the 8 ANSI colors, for a bare console or any terminal
// that reports fewer than 16 colors. Reverse video stands in for the shaded
// backgrounds the default theme relies on.
func MinimalTheme() Theme {
	base := tcell.StyleDefault
	return Theme{
		Text:          base,
		CursorLine:    base,
		Selection:     base.Reverse(true),
		Match:         base.Underline(true).Bold(true),
		Gutter:        base.Dim(true),
		GutterCurrent: base.Bold(true),
		Status:        base.Reverse(true),
		StatusMod:     base.Reverse(true).Bold(true),
		StatusDim:     base.Reverse(true),
		Message:       base,
		Error:         base.Foreground(tcell.ColorRed).Bold(true),

		Keyword: base.Foreground(tcell.ColorBlue),
		String:  base.Foreground(tcell.ColorRed),
		Comment: base.Foreground(tcell.ColorGreen),
		Number:  base.Foreground(tcell.ColorGreen),
		Type:    base.Foreground(tcell.ColorTeal),
		Func:    base.Foreground(tcell.ColorYellow),
		Preproc: base.Foreground(tcell.ColorPurple),

		GitAdded:    base.Foreground(tcell.ColorGreen),
		GitModified: base.Foreground(tcell.ColorBlue),
		GitDeleted:  base.Foreground(tcell.ColorRed),
	}
}

// ThemeFor picks a theme the given terminal can actually render.
func ThemeFor(c Caps) Theme {
	t, _ := NamedTheme(DefaultThemeName, c)
	return t
}

// DefaultThemeName is the theme used until the user picks another.
const DefaultThemeName = "dark"

// themes are the 256-color themes by name. Each paints only accents and
// leaves body text in the terminal's own colors, as DefaultTheme explains, so
// "light" is for terminals with a light background rather than one that
// paints its own.
var themes = map[string]func() Theme{
	"dark":          DefaultTheme,
	"light":         lightTheme,
	"gruvbox":       gruvboxTheme,
	"solarized":     solarizedTheme,
	"high-contrast": highContrastTheme,
}

// ThemeNames lists the themes, default first.
func ThemeNames() []string {
	return []string{"dark", "light", "gruvbox", "solarized", "high-contrast"}
}

// NamedTheme returns the theme called name, or the default and false when
// there is none. A terminal with fewer than 256 colors gets the minimal theme
// whatever was asked for: the others would collapse into unreadable nearest
// matches there.
func NamedTheme(name string, c Caps) (Theme, bool) {
	f, ok := themes[name]
	if !ok {
		f = themes[DefaultThemeName]
	}
	if c.Colors < 256 {
		return MinimalTheme(), ok
	}
	return f(), ok
}

// accents builds a theme from the handful of colors that distinguish one from
// another; the structure is the same for all of them.
type accents struct {
	cursorLine, selection, matchBg, matchFg     int32
	gutter, gutterCur                           int32
	barBg, barFg, barMod, barDim, message, err  int32
	keyword, str, comment, number, typ, fn, pre int32
	added, modified, deleted                    int32
}

func (c accents) theme() Theme {
	base := tcell.StyleDefault
	return Theme{
		Text:          base,
		CursorLine:    base.Background(pal(c.cursorLine)),
		Selection:     base.Background(pal(c.selection)),
		Match:         base.Background(pal(c.matchBg)).Foreground(pal(c.matchFg)),
		Gutter:        base.Foreground(pal(c.gutter)),
		GutterCurrent: base.Foreground(pal(c.gutterCur)),
		Status:        base.Background(pal(c.barBg)).Foreground(pal(c.barFg)),
		StatusMod:     base.Background(pal(c.barBg)).Foreground(pal(c.barMod)).Bold(true),
		StatusDim:     base.Background(pal(c.barBg)).Foreground(pal(c.barDim)),
		Message:       base.Foreground(pal(c.message)),
		Error:         base.Foreground(pal(c.err)).Bold(true),

		GitAdded:    base.Foreground(pal(c.added)),
		GitModified: base.Foreground(pal(c.modified)),
		GitDeleted:  base.Foreground(pal(c.deleted)),

		Keyword: base.Foreground(pal(c.keyword)),
		String:  base.Foreground(pal(c.str)),
		Comment: base.Foreground(pal(c.comment)),
		Number:  base.Foreground(pal(c.number)),
		Type:    base.Foreground(pal(c.typ)),
		Func:    base.Foreground(pal(c.fn)),
		Preproc: base.Foreground(pal(c.pre)),
	}
}

func lightTheme() Theme {
	return accents{
		cursorLine: 255, selection: 153, matchBg: 229, matchFg: 16,
		gutter: 246, gutterCur: 238,
		barBg: 252, barFg: 235, barMod: 130, barDim: 243, message: 235, err: 160,
		keyword: 25, str: 124, comment: 28, number: 90, typ: 30, fn: 94, pre: 127,
		added: 28, modified: 25, deleted: 160,
	}.theme()
}

func gruvboxTheme() Theme {
	return accents{
		cursorLine: 237, selection: 239, matchBg: 214, matchFg: 235,
		gutter: 243, gutterCur: 223,
		barBg: 237, barFg: 223, barMod: 214, barDim: 245, message: 223, err: 167,
		keyword: 167, str: 142, comment: 245, number: 175, typ: 214, fn: 108, pre: 208,
		added: 142, modified: 109, deleted: 167,
	}.theme()
}

func solarizedTheme() Theme {
	return accents{
		cursorLine: 235, selection: 236, matchBg: 136, matchFg: 234,
		gutter: 240, gutterCur: 245,
		barBg: 235, barFg: 245, barMod: 136, barDim: 240, message: 245, err: 160,
		keyword: 64, str: 37, comment: 240, number: 125, typ: 136, fn: 33, pre: 166,
		added: 64, modified: 33, deleted: 160,
	}.theme()
}

func highContrastTheme() Theme {
	t := accents{
		cursorLine: 234, selection: 21, matchBg: 226, matchFg: 16,
		gutter: 250, gutterCur: 231,
		barBg: 231, barFg: 16, barMod: 160, barDim: 238, message: 231, err: 196,
		keyword: 81, str: 214, comment: 250, number: 219, typ: 121, fn: 229, pre: 213,
		added: 46, modified: 51, deleted: 196,
	}.theme()
	t.Selection = t.Selection.Foreground(pal(231))
	t.GutterCurrent = t.GutterCurrent.Bold(true)
	return t
}
