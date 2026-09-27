// Command gen builds the parts of the sonarc website that follow the
// repository: the docs, rendered from docs/*.md and CONTRIBUTING.md with a
// search index, and the releases page, read from GitHub. The Pages workflow
// runs it on every change to those files and after every release, so the
// site never drifts from the repo.
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
	"time"
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
	s := site{root: root, out: out, repo: repo, commit: short(os.Getenv("GITHUB_SHA"))}
	if err := s.docs(); err != nil {
		return err
	}
	items, err := s.releases()
	if err != nil {
		return err
	}
	return markLatest(filepath.Join(out, "index.html"), items)
}

type site struct {
	root, out, repo, commit string
}

func (s site) releases() ([]releaseView, error) {
	rels, err := fetchReleases(s.repo)
	if err != nil {
		return nil, err
	}
	var items []releaseView
	for _, r := range rels {
		if r.Draft {
			continue
		}
		notes, err := s.render([]byte(r.Body), linker{site: s, base: "../"}, nil)
		if err != nil {
			return nil, fmt.Errorf("release %s: %w", r.Tag, err)
		}
		v := releaseView{Tag: r.Tag, Name: r.Name, URL: r.URL, Pre: r.Prerelease, Notes: notes.HTML}
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
		Title: "Releases", Kind: "releases", Base: "../", Repo: s.repo, Commit: s.commit, Releases: items,
		Lede: "Static binaries for Linux and macOS on amd64 and arm64, each release with a checksums.txt. The install script always fetches the latest.",
	}
	return items, write(filepath.Join(s.out, "releases", "index.html"), p)
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
	Nav                     []navItem
	Source, Repo, Commit    string
	Releases                []releaseView
}

type navItem struct {
	Title, URL string
	Current    bool
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
	return writeFile(path, buf.Bytes())
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
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
