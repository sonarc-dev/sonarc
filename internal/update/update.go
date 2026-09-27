// Package update finds sonarc's latest release and replaces the running
// binary with it: the same download and checksum check install.sh does, from
// inside the editor, so a server can be brought up to date with
// `sonarc -update` and nothing else.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is where releases are published.
const Repo = "sonarc-dev/sonarc"

// Source says where to look. The zero value is GitHub; tests point it at a
// local server.
type Source struct {
	// API is the GitHub API base, https://api.github.com by default.
	API string
	// Download is the base that release files are under, one directory per
	// tag: https://github.com/sonarc-dev/sonarc/releases/download by default.
	Download string
	Client   *http.Client
}

func (s Source) api() string {
	if s.API != "" {
		return strings.TrimSuffix(s.API, "/")
	}
	return "https://api.github.com"
}

func (s Source) download() string {
	if s.Download != "" {
		return strings.TrimSuffix(s.Download, "/")
	}
	return "https://github.com/" + Repo + "/releases/download"
}

func (s Source) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// Latest returns the tag of the newest release that is not a pre-release.
func (s Source) Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", s.api()+"/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("checking for a new version: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checking for a new version: GitHub answered %s", resp.Status)
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil || rel.Tag == "" {
		return "", errors.New("checking for a new version: unexpected answer from GitHub")
	}
	return rel.Tag, nil
}

// Asset is the release file for this machine, such as sonarc-linux-amd64.
func Asset() string { return "sonarc-" + runtime.GOOS + "-" + runtime.GOARCH }

// Install downloads tag's binary for this machine, checks it against the
// release's checksums.txt and replaces exe with it. The new file is written
// beside exe and renamed over it, so a failure at any point leaves the old
// binary in place, and a sonarc that is running keeps running.
func (s Source) Install(ctx context.Context, tag, exe string) error {
	name := Asset()
	base := s.download() + "/" + tag

	sums, err := s.fetch(ctx, base+"/checksums.txt", 1<<20)
	if err != nil {
		return err
	}
	want, ok := checksum(sums, name)
	if !ok {
		return fmt.Errorf("release %s has no binary for %s/%s", tag, runtime.GOOS, runtime.GOARCH)
	}

	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".sonarc-update-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w (install again with install.sh, or run as the user who owns it)", dir, err)
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed

	if err := s.fetchTo(ctx, base+"/"+name, tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	got, err := fileSum(tmp.Name())
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s; nothing was changed", name, want, got)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), exe)
}

func (s Source) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	return resp, nil
}

func (s Source) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := s.get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (s Source) fetchTo(ctx context.Context, url string, w io.Writer) error {
	resp, err := s.get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	return nil
}

// checksum finds name's sha256 in a sha256sum listing.
func checksum(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Newer reports whether release tag is a later version than current. Both
// are vMAJOR.MINOR.PATCH, optionally with a -suffix; a release is later than
// a pre-release of the same version. A current version that is not a release
// (a development build) is never considered out of date.
func Newer(tag, current string) bool {
	t, ok1 := parse(tag)
	c, ok2 := parse(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if t.n[i] != c.n[i] {
			return t.n[i] > c.n[i]
		}
	}
	return t.pre == "" && c.pre != ""
}

type semver struct {
	n   [3]int
	pre string
}

func parse(v string) (semver, bool) {
	var s semver
	v, ok := strings.CutPrefix(v, "v")
	if !ok {
		return s, false
	}
	v, s.pre, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return s, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return s, false
		}
		s.n[i] = n
	}
	return s, true
}
