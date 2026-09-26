// Command gen builds the parts of the sonarc website that follow the
// repository: the docs, rendered from README.md and CONTRIBUTING.md, and the
// releases page, read from GitHub. The Pages workflow runs it on every change
// to those files and after every release, so the site never drifts from the
// repo.
//
//	go run . -root ../.. -out ../../_site
//
// GITHUB_TOKEN, if set, authenticates the releases request; GITHUB_REPOSITORY
// and GITHUB_SHA, set by GitHub Actions, name the repository and the commit
// the pages were built from.
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

//go:embed page.html
var files embed.FS

var page = template.Must(template.ParseFS(files, "page.html"))

func main() {
	root := flag.String("root", "../..", "the repository checkout")
	out := flag.String("out", "../../_site", "the site being built; index.html in it gets the latest version")
	repo := flag.String("repo", envOr("GITHUB_REPOSITORY", "sonarc-dev/sonarc"), "owner/name on GitHub")
	flag.Parse()

	if err := build(*root, *out, *repo); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func build(root, out, repo string) error {
	sha := os.Getenv("GITHUB_SHA")
	g := site{repo: repo, sha: sha}

	docs := []struct {
		src, dir, title, lede string
	}{
		{"README.md", "docs", "Documentation", "Everything sonarc does, every key, and how it works. Built from the README on every change."},
		{"CONTRIBUTING.md", "docs/contributing", "Contributing", "How to build, test and send a change."},
	}
	for _, d := range docs {
		src, err := os.ReadFile(filepath.Join(root, d.src))
		if err != nil {
			return err
		}
		body, toc, err := g.markdown(src, true)
		if err != nil {
			return fmt.Errorf("%s: %w", d.src, err)
		}
		depth := strings.Count(d.dir, "/") + 1
		p := pageData{
			Title: d.title, Lede: d.lede, Kind: "docs", Base: strings.Repeat("../", depth),
			Body: body, TOC: toc, Source: d.src, Repo: repo, Commit: short(sha),
			OtherDocs: d.src != "README.md",
		}
		if err := write(filepath.Join(out, d.dir, "index.html"), p); err != nil {
			return err
		}
	}

	rels, err := fetchReleases(repo)
	if err != nil {
		return err
	}
	var items []releaseView
	for _, r := range rels {
		if r.Draft {
			continue
		}
		notes, _, err := g.markdown([]byte(r.Body), false)
		if err != nil {
			return fmt.Errorf("release %s: %w", r.Tag, err)
		}
		v := releaseView{Tag: r.Tag, Name: r.Name, URL: r.URL, Pre: r.Prerelease, Notes: notes}
		if t, err := time.Parse(time.RFC3339, r.Published); err == nil {
			v.Date, v.ISO = t.Format("2 January 2006"), t.Format("2006-01-02")
		}
		for _, a := range r.Assets {
			v.Assets = append(v.Assets, assetView{Name: a.Name, URL: a.URL, Size: size(a.Size)})
		}
		items = append(items, v)
	}
	if len(items) > 0 {
		items[0].Latest = !items[0].Pre
	}
	p := pageData{
		Title: "Releases", Kind: "releases", Base: "../", Repo: repo, Commit: short(sha), Releases: items,
		Lede: "Static binaries for Linux and macOS on amd64 and arm64, each release with a checksums.txt. The install script always fetches the latest.",
	}
	if err := write(filepath.Join(out, "releases", "index.html"), p); err != nil {
		return err
	}
	return markLatest(filepath.Join(out, "index.html"), items)
}

// markLatest puts the latest version next to the landing page's heading, so
// the front page says what the install line will fetch.
func markLatest(index string, items []releaseView) error {
	data, err := os.ReadFile(index)
	if os.IsNotExist(err) {
		return nil // building the docs alone
	}
	if err != nil {
		return err
	}
	link := ""
	for _, r := range items {
		if !r.Pre {
			link = fmt.Sprintf(` · <a href="releases/">%s</a>`, template.HTMLEscapeString(r.Tag))
			break
		}
	}
	return os.WriteFile(index, bytes.Replace(data, []byte("<!--latest-->"), []byte(link), 1), 0o644)
}

type site struct {
	repo, sha string
}

type tocItem struct {
	ID, Text string
	Sub      []tocItem
}

// markdown renders GitHub-flavored markdown. Links to files in the repository
// point at GitHub, except links to README.md, which is the docs page itself.
// With headings set, h2 and h3 get anchors and are returned as a contents
// list, and the document's own h1 is dropped in favor of the page's title.
func (g site) markdown(src []byte, headings bool) (template.HTML, []tocItem, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	doc := md.Parser().Parse(text.NewReader(src))

	var toc []tocItem
	var drop []ast.Node
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			if !headings {
				return ast.WalkContinue, nil
			}
			if n.Level == 1 {
				drop = append(drop, n)
				return ast.WalkSkipChildren, nil
			}
			id, _ := n.AttributeString("id")
			idStr, _ := id.([]byte)
			item := tocItem{ID: string(idStr), Text: plain(n, src)}
			switch {
			case n.Level == 2:
				toc = append(toc, item)
			case n.Level == 3 && len(toc) > 0:
				toc[len(toc)-1].Sub = append(toc[len(toc)-1].Sub, item)
			}
			if n.Level <= 3 {
				a := ast.NewLink()
				a.Destination = append([]byte("#"), idStr...)
				a.SetAttributeString("class", []byte("anchor"))
				a.SetAttributeString("aria-label", []byte("Link to this section"))
				a.AppendChild(a, ast.NewString([]byte("#")))
				n.AppendChild(n, a)
			}
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			n.Destination = []byte(g.link(string(n.Destination)))
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return "", nil, err
	}
	for _, n := range drop {
		n.Parent().RemoveChild(n.Parent(), n)
	}
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return "", nil, err
	}
	return template.HTML(buf.String()), toc, nil
}

var external = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`)

// link maps a link written for GitHub to one that works on the site.
func (g site) link(dest string) string {
	if dest == "" || strings.HasPrefix(dest, "#") || external.MatchString(dest) {
		return dest
	}
	path, frag, _ := strings.Cut(dest, "#")
	if frag != "" {
		frag = "#" + frag
	}
	switch strings.TrimPrefix(path, "./") {
	case "README.md":
		return "../" + frag // only CONTRIBUTING links to it, from docs/contributing/
	case "CONTRIBUTING.md":
		return "contributing/" + frag
	}
	return "https://github.com/" + g.repo + "/blob/main/" + strings.TrimPrefix(path, "./") + frag
}

// plain is a heading's text without markup, for the contents list.
func plain(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch c := c.(type) {
			case *ast.Text:
				b.Write(c.Segment.Value(src))
			case *ast.String:
				b.Write(c.Value)
			}
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

type release struct {
	Tag        string `json:"tag_name"`
	Name       string `json:"name"`
	Body       string `json:"body"`
	URL        string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Published  string `json:"published_at"`
	Assets     []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// fetchReleases lists the repository's releases, newest first. A failure
// stops the build: deploying an empty releases page would hide every release.
func fetchReleases(repo string) ([]release, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/repos/"+repo+"/releases?per_page=50", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("releases: GitHub answered %s", resp.Status)
	}
	var rels []release
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, fmt.Errorf("releases: %w", err)
	}
	return rels, nil
}

type pageData struct {
	Title, Lede, Kind, Base string
	Body                    template.HTML
	TOC                     []tocItem
	Source, Repo, Commit    string
	OtherDocs               bool
	Releases                []releaseView
}

type releaseView struct {
	Tag, Name, URL, Date, ISO string
	Pre, Latest               bool
	Notes                     template.HTML
	Assets                    []assetView
}

type assetView struct {
	Name, URL, Size string
}

func write(path string, p pageData) error {
	var buf bytes.Buffer
	if err := page.Execute(&buf, p); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
