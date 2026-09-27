// Package update finds sonarc's latest release and replaces the running
// binary with it: the same download and checksum check install.sh does, from
// inside the editor, so a server can be brought up to date with
// `sonarc -update` and nothing else.
package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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

// Latest returns the tag of the newest release that is not a pre-release.
func (s Source) Latest(ctx context.Context) (string, error) {
	body, err := fetch(ctx, s.api()+"/repos/"+Repo+"/releases/latest", 1<<20)
	if err != nil {
		return "", fmt.Errorf("checking for a new version: %w", err)
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil || rel.Tag == "" {
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

	sums, err := fetch(ctx, base+"/checksums.txt", 1<<20)
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

	if err := fetchTo(ctx, base+"/"+name, tmp); err != nil {
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

// Downloads go through curl or wget, whichever is installed, as install.sh's
// do. Go's own HTTP client would work too, but TLS and HTTP/2 double the size
// of the binary for three requests an editor makes once a day at most.

// fetch returns what url answers, up to limit bytes.
func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	var buf capped
	buf.limit = limit
	if err := fetchTo(ctx, url, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fetchTo writes what url answers to w. Anything but a 200 is an error.
func fetchTo(ctx context.Context, url string, w io.Writer) error {
	var cmd *exec.Cmd
	if curl, err := exec.LookPath("curl"); err == nil {
		cmd = exec.CommandContext(ctx, curl, "-fsSL", "--proto", "=https,http", "-H", "Accept: application/vnd.github+json", url)
	} else if wget, err := exec.LookPath("wget"); err == nil {
		cmd = exec.CommandContext(ctx, wget, "-q", "-O", "-", "--header=Accept: application/vnd.github+json", url)
	} else {
		return errors.New("downloading needs curl or wget, and neither is installed")
	}
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = w, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("downloading %s: %w", url, ctx.Err())
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = errors.New(msg)
		}
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	return nil
}

// capped is a buffer that refuses to grow past limit, so a broken or hostile
// answer cannot fill memory.
type capped struct {
	bytes.Buffer
	limit int64
}

func (c *capped) Write(p []byte) (int, error) {
	if int64(c.Len()+len(p)) > c.limit {
		return 0, errors.New("answer too large")
	}
	return c.Buffer.Write(p)
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
