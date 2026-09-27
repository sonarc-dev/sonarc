package provider

import (
	"context"
	"errors"
	"time"
)

// Doc is a file as it is in the editor, unsaved edits included: what a
// language server needs to answer about a place in it.
type Doc struct {
	Path    string
	Version uint64 // changes whenever the text does
	Text    []byte
}

// At is a place in a Doc: a 0-based line and a byte offset within it.
type At struct {
	Doc  Doc
	Line int
	Col  int
}

// PositionIntel is implemented by providers that answer about a place in a
// file rather than about a name: language servers. They know which of several
// same-named symbols the cursor is on, so their answer is the precise one.
type PositionIntel interface {
	// Handles reports whether the provider covers the file's language.
	Handles(path string) bool
	DefinitionAt(ctx context.Context, at At) ([]Symbol, error)
	ReferencesAt(ctx context.Context, at At) ([]Location, error)
	// HoverAt is a short description of what is at the position, such as a
	// function's signature; empty when there is nothing to say.
	HoverAt(ctx context.Context, at At) (string, error)
}

// namer is implemented by a provider that answers through different programs
// for different files, so an answer can name the one that gave it.
type namer interface {
	NameFor(path string) string
}

func nameFor(p PositionIntel, path string) string {
	if n, ok := p.(namer); ok {
		return n.NameFor(path)
	}
	return p.(CodeIntel).Name()
}

// positional returns the providers that can answer about at's file.
func (r *Registry) positional(path string) []PositionIntel {
	var out []PositionIntel
	for _, p := range r.list() {
		if pi, ok := p.(PositionIntel); ok && p.Available() && pi.Handles(path) {
			out = append(out, pi)
		}
	}
	return out
}

// HandlesPosition reports whether any provider answers about places in the
// file at path. The editor only copies a file's text for a query when one does.
func (r *Registry) HandlesPosition(path string) bool {
	return path != "" && len(r.positional(path)) > 0
}

// DefinitionsAt finds the definition of what is at a place in a file. A
// provider that understands the file's language answers first, and when it
// finds something that is the answer: it has resolved which symbol is meant,
// where a name-based index can only list everything with that name. Otherwise
// the name-based providers are asked for sym, as Definitions does.
func (r *Registry) DefinitionsAt(ctx context.Context, at At, sym string) []Symbol {
	for _, p := range r.positional(at.Doc.Path) {
		syms, err := p.DefinitionAt(ctx, at)
		if err == nil && len(syms) > 0 {
			name := nameFor(p, at.Doc.Path)
			for i := range syms {
				if syms[i].Source == "" {
					syms[i].Source = name
				}
				if syms[i].Name == "" {
					syms[i].Name = sym
				}
			}
			return dedupeSymbols(syms)
		}
		if ctx.Err() != nil {
			return nil
		}
	}
	if sym == "" {
		return nil
	}
	return r.Definitions(ctx, sym)
}

// ReferencesReportAt finds uses of what is at a place in a file. Language
// servers' answers are merged with the indexes', since a server may only know
// the files it has open; a whole-tree scan is still skipped once anything has
// answered.
func (r *Registry) ReferencesReportAt(ctx context.Context, at At, sym string) ([]Location, Report) {
	var out []Location
	var rep Report
	for _, p := range r.positional(at.Doc.Path) {
		start := time.Now()
		locs, err := p.ReferencesAt(ctx, at)
		t := Timing{Provider: nameFor(p, at.Doc.Path), Elapsed: time.Since(start), Count: len(locs)}
		if err != nil {
			t.Err, t.Count, locs = err, 0, nil
			if errors.Is(err, context.Canceled) {
				return nil, rep
			}
		}
		rep.Timings = append(rep.Timings, t)
		out = append(out, locs...)
	}
	if sym == "" {
		return dedupeLocations(out), rep
	}
	more, rest := r.referencesReport(ctx, sym, len(out) > 0)
	rep.Timings = append(rep.Timings, rest.Timings...)
	return dedupeLocations(append(out, more...)), rep
}

// HoverAt is the first description a provider gives of what is at a place.
func (r *Registry) HoverAt(ctx context.Context, at At) (string, string) {
	for _, p := range r.positional(at.Doc.Path) {
		if s, err := p.HoverAt(ctx, at); err == nil && s != "" {
			return s, nameFor(p, at.Doc.Path)
		}
	}
	return "", ""
}
