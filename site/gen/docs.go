package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// docPage is one page of the docs: its source file in the repository, where
// it goes on the site, and how the docs index describes it.
type docPage struct {
	Src, URL, Title, Lede string
}

// navLine is a line of docs/README.md naming a page:
//
//   - [Keys](keys.md): every key, grouped by what you are doing.
var navLine = regexp.MustCompile(`^- \[([^\]]+)\]\(([^)#]+\.md)\):?\s*(.*)$`)

// docPages reads the page list, in order, from docs/README.md. That file is
// also what GitHub shows for the docs folder, so the order people browse in
// is the same on both.
func (s site) docPages() ([]docPage, error) {
	index, err := os.ReadFile(filepath.Join(s.root, "docs", "README.md"))
	if err != nil {
		return nil, err
	}
	pages := []docPage{{
		Src: "docs/README.md", URL: "docs/", Title: "Documentation",
		Lede: "Everything sonarc does, every key, and how it works, kept in step with the code.",
	}}
	for _, line := range strings.Split(string(index), "\n") {
		m := navLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		src := path.Clean(path.Join("docs", m[2]))
		slug := strings.ToLower(strings.TrimSuffix(path.Base(src), ".md"))
		pages = append(pages, docPage{
			Src: src, URL: "docs/" + slug + "/", Title: m[1],
			Lede: strings.TrimSuffix(strings.ReplaceAll(m[3], "`", ""), "."),
		})
	}
	if len(pages) == 1 {
		return nil, fmt.Errorf("docs/README.md lists no pages")
	}
	return pages, nil
}

// searchEntry is one section of one page in docs/search.json.
type searchEntry struct {
	Page    string `json:"p"`
	Heading string `json:"h,omitempty"`
	URL     string `json:"u"`
	Text    string `json:"x"`
}

func (s site) docs() error {
	pages, err := s.docPages()
	if err != nil {
		return err
	}
	where := map[string]string{}
	for _, p := range pages {
		where[p.Src] = p.URL
	}

	var index []searchEntry
	for i, p := range pages {
		src, err := os.ReadFile(filepath.Join(s.root, p.Src))
		if err != nil {
			return err
		}
		base := strings.Repeat("../", strings.Count(p.URL, "/"))
		r, err := s.render(src, linker{site: s, from: p.Src, base: base, pages: where}, &headings{})
		if err != nil {
			return fmt.Errorf("%s: %w", p.Src, err)
		}
		title := p.Title
		if i > 0 && r.Title != "" {
			title = r.Title
		}
		var nav []navItem
		for j, q := range pages {
			nav = append(nav, navItem{Title: q.Title, URL: base + q.URL, Current: j == i})
		}
		lede := p.Lede
		if lede != "" {
			lede += "."
		}
		data := pageData{
			Title: title, Lede: strings.ToUpper(lede[:1]) + lede[1:], Kind: "docs", Base: base,
			Body: r.HTML, TOC: r.TOC, Nav: nav, Source: p.Src, Repo: s.repo, Commit: s.commit,
		}
		if err := write(filepath.Join(s.out, filepath.FromSlash(p.URL), "index.html"), data); err != nil {
			return err
		}
		for _, sec := range r.Sections {
			text := strings.Join(strings.Fields(sec.Text.String()), " ")
			if text == "" && sec.Heading == "" {
				continue
			}
			u := p.URL
			if sec.ID != "" {
				u += "#" + sec.ID
			}
			index = append(index, searchEntry{Page: title, Heading: sec.Heading, URL: u, Text: text})
		}
	}
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(s.out, "docs", "search.json"), data)
}
