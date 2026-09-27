package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// release serves a fake GitHub: the latest-release answer and one release's
// files, with the binary's checksum as given.
func release(t *testing.T, tag string, binary []byte, sum string) Source {
	t.Helper()
	if sum == "" {
		h := sha256.Sum256(binary)
		sum = hex.EncodeToString(h[:])
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"` + tag + `","name":"x"}`))
	})
	mux.HandleFunc("/dl/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0000  sonarc-other-arch\n" + sum + "  " + Asset() + "\n"))
	})
	mux.HandleFunc("/dl/"+tag+"/"+Asset(), func(w http.ResponseWriter, r *http.Request) {
		w.Write(binary)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return Source{API: srv.URL, Download: srv.URL + "/dl"}
}

func TestLatest(t *testing.T) {
	src := release(t, "v1.2.3", nil, "")
	tag, err := src.Latest(context.Background())
	if err != nil || tag != "v1.2.3" {
		t.Fatalf("Latest = %q, %v", tag, err)
	}
}

func TestInstallReplacesTheBinary(t *testing.T) {
	src := release(t, "v1.2.3", []byte("NEW BINARY"), "")
	exe := filepath.Join(t.TempDir(), "sonarc")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := src.Install(context.Background(), "v1.2.3", exe); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(exe)
	fi, _ := os.Stat(exe)
	if string(data) != "NEW BINARY" || fi.Mode().Perm() != 0o755 {
		t.Errorf("exe = %q, mode %v", data, fi.Mode())
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".sonarc-update-*"))
	if len(left) > 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

// A download that does not match the published checksum must leave the old
// binary exactly as it was.
func TestInstallRefusesABadChecksum(t *testing.T) {
	src := release(t, "v1.2.3", []byte("TAMPERED"), strings.Repeat("ab", 32))
	exe := filepath.Join(t.TempDir(), "sonarc")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := src.Install(context.Background(), "v1.2.3", exe)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	if data, _ := os.ReadFile(exe); string(data) != "OLD" {
		t.Errorf("exe = %q, want it untouched", data)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".sonarc-update-*"))
	if len(left) > 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestInstallReportsAMissingPlatform(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/dl/v1.0.0/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("abcd  sonarc-plan9-mips\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	exe := filepath.Join(t.TempDir(), "sonarc")
	os.WriteFile(exe, []byte("OLD"), 0o755)
	err := Source{Download: srv.URL + "/dl"}.Install(context.Background(), "v1.0.0", exe)
	if err == nil || !strings.Contains(err.Error(), "no binary for") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstallNeedsCurlOrWget(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Source{API: "http://127.0.0.1:1"}.Latest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "curl or wget") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		tag, cur string
		want     bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v1.0.0", "v0.9.9", true},
		{"v0.10.0", "v0.9.0", true},
		{"v0.2.0", "v0.2.0-rc1", true},
		{"v0.2.0-rc1", "v0.2.0", false},
		{"v0.2.0", "dev", false},
		{"v0.2.0", "5f9ee28-dirty", false},
		{"garbage", "v0.1.0", false},
	} {
		if got := Newer(c.tag, c.cur); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.tag, c.cur, got, c.want)
		}
	}
}
