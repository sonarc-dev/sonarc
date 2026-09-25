package tags

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// memoryLimit is the size below which the whole file is just loaded. Small
	// tags files are common (a single project, a single directory) and an
	// in-memory map is both simpler and faster for them.
	memoryLimit = 8 << 20

	// blockSize is where the binary search stops halving and scans linearly.
	// One block is a single read, so making it smaller costs more seeks than it
	// saves scanning.
	blockSize = 16 << 10

	// maxLine bounds how much is read looking for a line terminator, so a
	// corrupt or binary file cannot cause an unbounded allocation.
	maxLine = 1 << 20
)

// Reader answers lookups against a tags file.
//
// It is safe for concurrent use: on-disk lookups go through ReadAt, which needs
// no shared cursor.
type Reader struct {
	path string
	base string // directory that relative file paths resolve against

	f    *os.File
	size int64

	sorted    bool // the file declares itself sorted by name
	foldCase  bool // sorted case-insensitively (!_TAG_FILE_SORTED 2)
	dataStart int64

	// mem is populated instead of f for small or unsorted files.
	mem map[string][]Entry

	mu     sync.RWMutex
	closed bool
}

// Open reads the header of a tags file and prepares it for lookups.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	r := &Reader{
		path: abs,
		base: filepath.Dir(abs),
		f:    f,
		size: fi.Size(),
	}
	if err := r.readHeader(); err != nil {
		f.Close()
		return nil, err
	}

	// Binary search requires a sorted file. Anything else has to be indexed in
	// memory, which is only acceptable when the file is small.
	if !r.sorted || r.size <= memoryLimit {
		if r.size > memoryLimit && !r.sorted {
			f.Close()
			return nil, fmt.Errorf("%s is %d MB and unsorted: re-run ctags without --sort=no",
				filepath.Base(path), r.size>>20)
		}
		if err := r.loadMemory(); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
		r.f = nil
	}
	return r, nil
}

// Path returns the tags file's path.
func (r *Reader) Path() string { return r.path }

// Sorted reports whether lookups use on-disk binary search.
func (r *Reader) Sorted() bool { return r.sorted }

// InMemory reports whether the file was small enough to load.
func (r *Reader) InMemory() bool { return r.mem != nil }

// Size returns the tags file size in bytes.
func (r *Reader) Size() int64 { return r.size }

// Close releases the file handle.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	r.mem = nil
	if r.f != nil {
		err := r.f.Close()
		r.f = nil
		return err
	}
	return nil
}

// readHeader parses the leading !_TAG_ pseudo-tags, which declare whether the
// file is sorted and how.
func (r *Reader) readHeader() error {
	buf := make([]byte, 4096)
	n, err := r.f.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		return err
	}
	buf = buf[:n]

	off := int64(0)
	for len(buf) > 0 {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break
		}
		line := string(buf[:i])
		if !strings.HasPrefix(line, "!_TAG_") {
			break // headers are contiguous and come first
		}
		if fields := strings.Split(line, "\t"); len(fields) >= 2 {
			switch fields[0] {
			case "!_TAG_FILE_SORTED":
				switch strings.TrimSpace(fields[1]) {
				case "1":
					r.sorted, r.foldCase = true, false
				case "2":
					r.sorted, r.foldCase = true, true
				default:
					r.sorted = false
				}
			case "!_TAG_PROC_CWD":
				// Newer ctags records the directory it ran in, which is what
				// relative paths in the file are relative to.
				if d := strings.TrimSpace(fields[1]); d != "" {
					r.base = d
				}
			}
		}
		off += int64(i + 1)
		buf = buf[i+1:]
	}
	r.dataStart = off
	return nil
}

// loadMemory reads the whole file into a name-keyed index.
func (r *Reader) loadMemory() error {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return err
	}
	m := make(map[string][]Entry)
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		if e, ok := parseEntry(string(raw)); ok {
			m[e.Name] = append(m[e.Name], e)
		}
	}
	r.mem = m
	return nil
}

// compare orders two tag names the way the file is sorted.
func (r *Reader) compare(a, b string) int {
	if r.foldCase {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	}
	return strings.Compare(a, b)
}

// readBufs recycles the scratch buffers used to scan for line terminators.
// A single lookup reads roughly twenty lines and completion re-runs on every
// keystroke, so allocating a buffer per line shows up in the profile.
var readBufs = sync.Pool{
	New: func() any {
		b := make([]byte, 4096)
		return &b
	},
}

// lineAt reads the line beginning at off, returning it without its terminator
// along with the offset of the next line.
func (r *Reader) lineAt(off int64) (string, int64, error) {
	if off >= r.size {
		return "", off, io.EOF
	}
	bufp := readBufs.Get().(*[]byte)
	defer readBufs.Put(bufp)
	buf := *bufp

	var sb []byte
	pos := off
	for {
		n, err := r.f.ReadAt(buf, pos)
		if n > 0 {
			if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
				if sb == nil {
					// Overwhelmingly the common case: the whole line arrived in
					// one read, so it converts to a string with one allocation
					// instead of being copied through an accumulator first.
					return string(bytes.TrimSuffix(buf[:i], []byte{'\r'})), pos + int64(i) + 1, nil
				}
				sb = append(sb, buf[:i]...)
				return string(bytes.TrimSuffix(sb, []byte{'\r'})), pos + int64(i) + 1, nil
			}
			sb = append(sb, buf[:n]...)
			pos += int64(n)
		}
		if err != nil {
			if err == io.EOF {
				if len(sb) == 0 {
					return "", pos, io.EOF
				}
				return string(sb), pos, nil
			}
			return "", pos, err
		}
		if len(sb) > maxLine {
			return "", pos, errors.New("tags: line too long; file may not be a tags file")
		}
	}
}

// lineAfter returns the first line that starts at or after off. Seeking into
// the middle of a line yields the following one, which is what the binary
// search needs to make progress.
func (r *Reader) lineAfter(off int64) (line string, start int64, next int64, err error) {
	if off <= r.dataStart {
		l, n, err := r.lineAt(r.dataStart)
		return l, r.dataStart, n, err
	}
	// Step back one byte so that landing exactly on a line start keeps that
	// line rather than skipping it.
	_, next, err = r.lineAt(off - 1)
	if err != nil {
		return "", off, off, err
	}
	l, n, err := r.lineAt(next)
	return l, next, n, err
}

// find locates the first line whose name is >= target, returning its offset.
//
// This is a binary search over byte offsets rather than over lines: each step
// halves the window and then snaps to the next line boundary. Halving the byte
// range guarantees termination, which a search over line boundaries does not,
// since lines vary in length. Once the window is down to one block it is
// scanned directly.
func (r *Reader) find(target string) (int64, error) {
	lo, hi := r.dataStart, r.size
	for hi-lo > blockSize {
		mid := lo + (hi-lo)/2
		line, start, next, err := r.lineAfter(mid)
		if err != nil || start >= hi {
			// No usable line in the upper half; the answer is below.
			hi = mid
			continue
		}
		if r.compare(entryName(line), target) < 0 {
			lo = next
		} else {
			hi = mid
		}
	}
	return lo, nil
}

// Lookup returns every entry whose name matches exactly.
func (r *Reader) Lookup(name string) ([]Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, errors.New("tags: reader is closed")
	}
	if r.mem != nil {
		out := make([]Entry, len(r.mem[name]))
		copy(out, r.mem[name])
		return out, nil
	}

	off, err := r.find(name)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for off < r.size {
		line, next, err := r.lineAt(off)
		if err != nil {
			break
		}
		off = next
		n := entryName(line)
		cmp := r.compare(n, name)
		if cmp < 0 {
			continue // still inside the scanned block, before the match
		}
		if cmp > 0 {
			break // past the run of matches
		}
		// With a case-insensitive sort, names that fold together are adjacent,
		// so an exact match still has to be checked.
		if n != name {
			continue
		}
		if e, ok := parseEntry(line); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// Prefix returns up to limit entries whose names start with prefix. This drives
// the symbol picker and tag-based completion.
func (r *Reader) Prefix(prefix string, limit int) ([]Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, errors.New("tags: reader is closed")
	}
	if limit <= 0 {
		limit = 100
	}
	match := func(name string) bool {
		if r.foldCase {
			return strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
		}
		return strings.HasPrefix(name, prefix)
	}

	if r.mem != nil {
		var out []Entry
		for name, entries := range r.mem {
			if !match(name) {
				continue
			}
			for _, e := range entries {
				if out = append(out, e); len(out) >= limit {
					return out, nil
				}
			}
		}
		return out, nil
	}

	off, err := r.find(prefix)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for off < r.size && len(out) < limit {
		line, next, err := r.lineAt(off)
		if err != nil {
			break
		}
		off = next
		name := entryName(line)
		if name == "" {
			continue
		}
		if !match(name) {
			// Names before the prefix can appear inside the scanned block;
			// only stop once we are genuinely past it.
			if r.compare(name, prefix) > 0 {
				break
			}
			continue
		}
		if e, ok := parseEntry(line); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// Resolve turns an entry's file field into an absolute path.
func (r *Reader) Resolve(e Entry) string {
	if filepath.IsAbs(e.File) {
		return e.File
	}
	return filepath.Join(r.base, e.File)
}
