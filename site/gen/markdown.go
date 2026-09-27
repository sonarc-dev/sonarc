package main

import (
	"bytes"
	"html/template"
	"path"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

type tocItem struct {
	ID, Text string
	Sub      []tocItem
}

// section is one heading's worth of a page, for the search index.
type section struct {
	ID, Heading string
	Text        strings.Builder
}

type rendered struct {
	HTML     template.HTML
	Title    string // the document's own h1
	TOC      []tocItem
	Sections []*section
}

// headings, when set, asks render to drop the h1 (the page shows its own
// title), anchor h2 and h3, and collect the contents list and search text.
type headings struct{}

// render turns GitHub-flavored markdown into HTML. Raw HTML in the source is
// left out, so release notes cannot inject markup into the site.
func (s site) render(src []byte, l linker, h *headings) (rendered, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	doc := md.Parser().Parse(text.NewReader(src))

	var r rendered
	var drop []ast.Node
	cur := &section{}
	r.Sections = append(r.Sections, cur)
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			if h == nil {
				return ast.WalkContinue, nil
			}
			if n.Level == 1 {
				r.Title = plain(n, src)
				drop = append(drop, n)
				return ast.WalkSkipChildren, nil
			}
			idv, _ := n.AttributeString("id")
			id, _ := idv.([]byte)
			item := tocItem{ID: string(id), Text: plain(n, src)}
			switch {
			case n.Level == 2:
				r.TOC = append(r.TOC, item)
			case n.Level == 3 && len(r.TOC) > 0:
				r.TOC[len(r.TOC)-1].Sub = append(r.TOC[len(r.TOC)-1].Sub, item)
			}
			if n.Level <= 3 {
				cur = &section{ID: item.ID, Heading: item.Text}
				r.Sections = append(r.Sections, cur)
				a := ast.NewLink()
				a.Destination = append([]byte("#"), id...)
				a.SetAttributeString("class", []byte("anchor"))
				a.SetAttributeString("aria-label", []byte("Link to this section"))
				a.AppendChild(a, ast.NewString([]byte("#")))
				n.AppendChild(n, a)
			}
			return ast.WalkSkipChildren, nil
		case *ast.Paragraph, *ast.TextBlock, *east.TableCell:
			if h != nil {
				cur.Text.WriteString(plain(n, src))
				cur.Text.WriteByte(' ')
			}
		case *ast.Link:
			n.Destination = []byte(l.link(string(n.Destination)))
		case *ast.AutoLink:
			// Left as is: an autolink is always a full URL.
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return r, err
	}
	for _, n := range drop {
		n.Parent().RemoveChild(n.Parent(), n)
	}
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return r, err
	}
	r.HTML = template.HTML(buf.String())
	return r, nil
}

// plain is a node's text without markup.
func plain(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch c := c.(type) {
			case *ast.Text:
				b.Write(c.Segment.Value(src))
				if c.SoftLineBreak() || c.HardLineBreak() {
					b.WriteByte(' ')
				}
			case *ast.String:
				b.Write(c.Value)
			}
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// linker maps links written for GitHub, relative to a file in the
// repository, to links that work on the site.
type linker struct {
	site
	from  string            // the source file, relative to the repository root
	base  string            // from the page being written to the site root
	pages map[string]string // repository path of each rendered page → its site path
}

var external = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`)

func (l linker) link(dest string) string {
	if dest == "" || strings.HasPrefix(dest, "#") || external.MatchString(dest) {
		return dest
	}
	p, frag, _ := strings.Cut(dest, "#")
	if frag != "" {
		frag = "#" + frag
	}
	target := path.Clean(path.Join(path.Dir(l.from), p))
	if target == "README.md" {
		return l.base + frag // the repository's front page is the site's
	}
	if site, ok := l.pages[target]; ok {
		return l.base + site + frag
	}
	return "https://github.com/" + l.repo + "/blob/main/" + target + frag
}
