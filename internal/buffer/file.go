package buffer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// largeFileBytes is the point past which per-line work such as syntax
// highlighting is suppressed, so opening a huge log doesn't hang the editor.
const largeFileBytes = 50 << 20

// utf8BOM is stripped on load and restored on save. Dropping it would silently
// change the file for anything that depends on it.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Open reads path into a new buffer. A path that does not exist yields an empty
// buffer bound to it, so opening a new file just works; the file is created on
// save.
func Open(path string) (*Buffer, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// Stamped before reading, so a write racing the read shows up as a
	// change on the next check rather than being missed.
	stamp := StampOf(abs)
	data, err := os.ReadFile(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			b := New()
			b.path = abs
			return b, nil
		}
		return nil, err
	}
	b := FromBytes(data)
	b.path = abs
	b.disk = stamp
	return b, nil
}

// Stamp identifies one version of a file on disk, well enough to notice that
// another program has written it.
type Stamp struct {
	Exists  bool
	Size    int64
	ModTime time.Time
}

// StampOf reads path's stamp; a missing or unreadable file does not exist.
func StampOf(path string) Stamp {
	fi, err := os.Stat(path)
	if err != nil {
		return Stamp{}
	}
	return Stamp{Exists: true, Size: fi.Size(), ModTime: fi.ModTime()}
}

// DiskState is how a buffer's file compares with what the buffer was read
// from or last wrote.
type DiskState int

const (
	DiskSame    DiskState = iota // nothing else has touched it
	DiskChanged                  // another program wrote it
	DiskGone                     // it was deleted or moved away
)

// OnDisk compares the file with the version this buffer knows about. A buffer
// with no file, or whose file has never existed, is always the same.
func (b *Buffer) OnDisk() (DiskState, Stamp) {
	if b.path == "" {
		return DiskSame, Stamp{}
	}
	now := StampOf(b.path)
	switch {
	case now == b.disk:
		return DiskSame, now
	case !now.Exists:
		return DiskGone, now
	}
	return DiskChanged, now
}

// AcceptDisk records s as the version on disk the buffer knows about, when
// the user has been told of a change and chosen to keep their own text.
func (b *Buffer) AcceptDisk(s Stamp) { b.disk = s }

// FromBytes builds a buffer from raw file contents, detecting the BOM and line
// endings needed to reproduce those bytes exactly on save.
func FromBytes(data []byte) *Buffer {
	b := &Buffer{large: len(data) > largeFileBytes}

	if bytes.HasPrefix(data, utf8BOM) {
		b.hadBOM = true
		data = data[len(utf8BOM):]
	}

	// Split on '\n', recording per line whether it was preceded by '\r'. Line
	// bytes alias data rather than being copied: edits allocate a fresh slice
	// for the line they touch, so the original stays intact.
	var lines []line
	crlfCount, lfCount := 0, 0
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		end := i
		crlf := end > start && data[end-1] == '\r'
		if crlf {
			end--
			crlfCount++
		} else {
			lfCount++
		}
		lines = append(lines, line{text: data[start:end], crlf: crlf})
		start = i + 1
	}
	if start < len(data) {
		// Trailing bytes with no terminator: a final line lacking a newline.
		lines = append(lines, line{text: data[start:]})
		b.finalEOL = false
	} else {
		b.finalEOL = len(data) > 0
	}
	if len(lines) == 0 {
		lines = []line{{}}
	}

	// New lines adopt whichever ending the file mostly uses.
	b.crlf = crlfCount > lfCount
	b.lines = lines
	return b
}

// Bytes renders the buffer back to file contents, restoring the BOM, each
// line's own terminator, and the presence or absence of a final newline. For an
// unedited buffer this reproduces the input to Open byte for byte.
func (b *Buffer) Bytes() []byte {
	n := b.NumLines()
	size := 0
	for i := 0; i < n; i++ {
		size += len(b.Line(i)) + 2
	}
	out := make([]byte, 0, size+len(utf8BOM))

	if b.hadBOM {
		out = append(out, utf8BOM...)
	}
	for i := 0; i < n; i++ {
		ln := b.at(i)
		out = append(out, ln.text...)
		if i < n-1 || b.finalEOL {
			if ln.crlf {
				out = append(out, '\r')
			}
			out = append(out, '\n')
		}
	}
	return out
}

// Save writes the buffer back to its own path.
func (b *Buffer) Save() error {
	if b.path == "" {
		return errors.New("buffer has no filename")
	}
	return b.SaveAs(b.path)
}

// SaveAs writes the buffer to path and binds the buffer to it.
//
// The write is atomic: contents go to a temporary file in the same directory,
// are flushed to disk, and are then renamed into place. A crash or a full disk
// therefore leaves the original file intact rather than truncated, which
// matters when the file being edited is a server's live config.
func (b *Buffer) SaveAs(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// Write through a symlink to its target instead of replacing the link,
	// but keep calling the file by the name it was opened as: that name is
	// how the file tree, git and the jump history know it, and a checkout
	// reached through a symlinked directory must not change identity on the
	// first save.
	name := abs
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}

	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(abs); err == nil {
		mode = fi.Mode().Perm()
	}

	dir := filepath.Dir(abs)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(abs)+".sonarc*")
	if err != nil {
		// A read-only directory is a common case on servers; report it plainly
		// rather than as a confusing temp-file error.
		return fmt.Errorf("cannot write in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Harmless if the rename already succeeded.
		os.Remove(tmpName)
	}()

	if _, err := tmp.Write(b.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return err
	}

	b.path = name
	b.disk = StampOf(name)
	b.markSaved()
	return nil
}
