package main

import (
	"hash/fnv"
	"os"
	"path/filepath"

	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/index/provider"
	"github.com/sonarc-dev/sonarc/internal/lsp"
)

// startLSP prepares the language servers for the project. Nothing runs yet:
// each server starts the first time a file in its language is asked about,
// and only if it is installed. SONARC_NO_LSP=1 leaves them all out.
func (a *app) startLSP() {
	if os.Getenv("SONARC_NO_LSP") != "" {
		return
	}
	var conf *os.File
	if dir := a.stateDir(); dir != "" {
		conf, _ = os.Open(filepath.Join(dir, "lsp.conf"))
	}
	var m *lsp.Manager
	var errs []error
	if conf != nil {
		m, errs = lsp.New(a.root, conf)
		conf.Close()
	} else {
		m, errs = lsp.New(a.root, nil)
	}
	a.lsp = m
	if len(errs) > 0 {
		a.ui.Error("lsp.conf %v", errs[0])
	}
}

// cursorAt is the cursor as a place a language server can answer about. It is
// false, and nothing is copied, when no server handles the file.
func (a *app) cursorAt() (provider.At, bool) {
	v := a.v()
	path := v.Buf.Path()
	if a.index == nil || v.Buf.Large() || !a.index.HandlesPosition(path) {
		return provider.At{}, false
	}
	text := v.Buf.Text(buffer.Pos{}, v.Buf.End())
	h := fnv.New64a()
	h.Write(text)
	return provider.At{
		// The version is the text's hash rather than the buffer's counter,
		// which starts again when a file is reloaded from disk.
		Doc:  provider.Doc{Path: path, Version: h.Sum64(), Text: text},
		Line: v.Head.Line,
		Col:  v.Head.Col,
	}, true
}
