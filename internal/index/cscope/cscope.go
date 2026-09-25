// Package cscope drives a cscope database for cross-referencing C code.
//
// The design decision that matters is that one cscope process is kept alive for
// the life of the editor and fed queries over its line-oriented interface.
// Spawning cscope per query would be far simpler, but on a kernel-sized
// database it re-reads the inverted index every time and each lookup costs
// seconds. A resident process answers in milliseconds. This is the same
// approach vim's cscope integration takes, and it is the difference between the
// feature being useful and being abandoned.
package cscope

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"sonarc/internal/proc"
)

// Query is a cscope search type, matching the digits its line interface takes.
type Query int

const (
	FindSymbol      Query = 0 // every reference to a symbol
	FindDefinition  Query = 1 // where a symbol is defined
	FindCallees     Query = 2 // functions called by this function
	FindCallers     Query = 3 // functions calling this function
	FindText        Query = 4 // a literal string
	_               Query = 5 // "change this text": destructive, not exposed
	FindEgrep       Query = 6 // an egrep pattern
	FindFile        Query = 7 // a file by name
	FindIncluding   Query = 8 // files that #include this one
	FindAssignments Query = 9 // assignments to a symbol
)

// String names the query for the UI.
func (q Query) String() string {
	switch q {
	case FindSymbol:
		return "references"
	case FindDefinition:
		return "definition"
	case FindCallees:
		return "functions called by"
	case FindCallers:
		return "callers"
	case FindText:
		return "text"
	case FindEgrep:
		return "egrep"
	case FindFile:
		return "file"
	case FindIncluding:
		return "files including"
	case FindAssignments:
		return "assignments"
	}
	return "query"
}

// Result is one line of cscope output.
type Result struct {
	File     string // path as cscope reports it, resolved against the db dir
	Function string // enclosing function, or "<global>"
	Line     int
	Text     string // the source line
}

// defaultTimeout bounds a single query. A query slower than this on a warm
// database means something is wrong, and the editor must not wait on it.
const defaultTimeout = 15 * time.Second

// DB is a live connection to a cscope database.
type DB struct {
	dir    string // directory holding cscope.out; relative paths resolve here
	dbPath string
	exe    string

	// Queries are serialized through this channel to the goroutine that owns
	// the subprocess pipes. cscope's protocol is strictly request/response, so
	// concurrent writers would interleave and desynchronize the stream.
	reqs chan *request
	// done is closed on shutdown. Query selects on it rather than the request
	// channel being closed: closing reqs would race with an in-flight Query
	// and panic with "send on closed channel" when the editor exits during a
	// search.
	done      chan struct{}
	closeOnce sync.Once

	mu      sync.Mutex
	cmd     *exec.Cmd
	reaper  *reaper // the one Wait for cmd; see reaperLocked
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	started bool
	failed  error
	closed  bool

	Timeout time.Duration
}

type request struct {
	ctx   context.Context
	query Query
	term  string
	reply chan response
}

type response struct {
	results []Result
	err     error
}

// Find locates a cscope database at or above dir, mirroring how cscope itself
// searches, and honors $CSCOPE_DB when it is set.
func Find(dir string) (string, bool) {
	if env := os.Getenv("CSCOPE_DB"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, true
		}
	}
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		p := filepath.Join(d, "cscope.out")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// Available reports whether the cscope binary is installed.
func Available() bool {
	_, err := exec.LookPath("cscope")
	return err == nil
}

// Open prepares a connection to the database at dbPath. The subprocess is
// started lazily on the first query, so opening an editor in a directory with a
// huge database costs nothing until navigation is actually used.
func Open(dbPath string) (*DB, error) {
	exe, err := exec.LookPath("cscope")
	if err != nil {
		return nil, fmt.Errorf("cscope is not installed: %w", err)
	}
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		abs = dbPath
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, err
	}
	db := &DB{
		dir:     filepath.Dir(abs),
		dbPath:  abs,
		exe:     exe,
		reqs:    make(chan *request),
		done:    make(chan struct{}),
		Timeout: defaultTimeout,
	}
	go db.serve()
	return db, nil
}

// Dir returns the directory the database covers.
func (d *DB) Dir() string { return d.dir }

// Path returns the database file path.
func (d *DB) Path() string { return d.dbPath }

// start launches the cscope subprocess. Callers hold no lock.
func (d *DB) start() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.started {
		return d.failed
	}
	d.started = true

	// -d: do not rebuild the database, just use it.
	// -l: line-oriented interface, which is the protocol implemented here.
	// -f: the database to use.
	cmd := exec.Command(d.exe, "-d", "-l", "-f", filepath.Base(d.dbPath))
	cmd.Dir = d.dir
	proc.SetGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		d.failed = err
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		d.failed = err
		return err
	}
	// cscope writes warnings to stderr; discard them rather than letting the
	// pipe fill and block the process.
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		d.failed = fmt.Errorf("cannot start cscope: %w", err)
		return d.failed
	}
	d.cmd = cmd
	d.stdin = stdin
	d.stdout = bufio.NewReaderSize(stdout, 64<<10)
	return nil
}

// restart tears down a broken subprocess so the next query starts a fresh one.
// cscope dying must degrade navigation, never take the editor with it.
func (d *DB) restart() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cmd != nil {
		if d.stdin != nil {
			d.stdin.Close()
		}
		r := d.reaperLocked()
		proc.KillTree(d.cmd)
		r.wait(time.Second)
	}
	d.cmd, d.reaper, d.stdin, d.stdout = nil, nil, nil, nil
	d.started, d.failed = false, nil
}

// serve owns the subprocess and answers requests one at a time.
func (d *DB) serve() {
	for {
		select {
		case <-d.done:
			return
		case req := <-d.reqs:
			// Its caller may have given up while it waited behind another
			// request. Running it would start a scan nobody will read.
			if err := req.ctx.Err(); err != nil {
				select {
				case req.reply <- response{nil, err}:
				default:
				}
				continue
			}
			results, err := d.exchange(req)
			// The caller may already have given up; never block delivering.
			select {
			case req.reply <- response{results, err}:
			default:
			}
		}
	}
}

// exchange performs one request/response cycle, restarting cscope if the
// exchange fails partway and leaves the stream in an unknown state.
func (d *DB) exchange(req *request) ([]Result, error) {
	if err := d.start(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	stdin, stdout, cmd := d.stdin, d.stdout, d.cmd
	d.mu.Unlock()
	if stdin == nil || stdout == nil || cmd == nil {
		return nil, errors.New("cscope: not running")
	}

	// A pattern containing a newline would be read as two commands and
	// desynchronize the protocol.
	term := strings.ReplaceAll(req.term, "\n", " ")
	if _, err := fmt.Fprintf(stdin, "%d%s\n", int(req.query), term); err != nil {
		d.restart()
		return nil, fmt.Errorf("cscope: write failed: %w", err)
	}

	// If the caller gives up — Esc, or the timeout — kill cscope rather than
	// waiting for it. Its text search reads every source file, one core, for
	// tens of seconds on a big tree; letting it drain kept a core busy for
	// nobody and made the next query queue behind it. Starting a fresh cscope
	// is cheap by comparison.
	stopKill := context.AfterFunc(req.ctx, func() { proc.KillTree(cmd) })
	results, err := readResponse(stdout)
	stopKill()

	if cerr := req.ctx.Err(); cerr != nil {
		// Killed, or finished just as the deadline passed; either way the
		// process is not in a state to reuse.
		d.restart()
		return nil, cerr
	}
	if err != nil {
		// The stream position is no longer known, so the process cannot be
		// reused for the next query.
		d.restart()
		return nil, err
	}
	return results, nil
}

// readResponse reads the "cscope: N lines" header and the N results after it.
//
// A query can also end without a header. cscope answers a symbol it has no
// entry for with "Unable to search database" — the usual outcome for a local
// variable or a typo — and some other failures the same way, each as the line
// its ">> " prompt starts. Waiting for a header that never comes made every
// such miss hang until the query timed out: 15 s on a kernel tree, every time.
func readResponse(r *bufio.Reader) ([]Result, error) {
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("cscope: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		n, ok := parseCount(line)
		if !ok {
			if msg, done := statusLine(line); done {
				if noMatch(msg) {
					return nil, nil
				}
				return nil, fmt.Errorf("cscope: %s", msg)
			}
			continue // a notice cscope printed before the prompt; not part of the answer
		}
		out := make([]Result, 0, n)
		for i := 0; i < n; i++ {
			l, err := r.ReadString('\n')
			if err != nil {
				return out, fmt.Errorf("cscope: truncated results: %w", err)
			}
			if res, ok := parseResult(strings.TrimRight(l, "\r\n")); ok {
				out = append(out, res)
			}
		}
		return out, nil
	}
}

// statusLine recognises a response that is a message rather than results: the
// prompt followed by text that is not a count header. It reports the message.
func statusLine(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, ">> ")
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(strings.TrimLeft(rest, "> "))
	if rest == "" {
		return "", false
	}
	return rest, true
}

// noMatch reports whether a status message just means "nothing found".
func noMatch(msg string) bool {
	return strings.HasPrefix(msg, "Unable to search database") ||
		strings.HasPrefix(msg, "This is not a C symbol") ||
		strings.HasPrefix(msg, "Could not find")
}

// parseCount recognizes the "cscope: N lines" header.
//
// The header is located anywhere in the line rather than only at its start:
// cscope writes its ">> " prompt without a trailing newline, so the prompt and
// the header routinely arrive as a single line of ">> cscope: 5 lines".
func parseCount(line string) (int, bool) {
	i := strings.Index(line, "cscope: ")
	if i < 0 {
		return 0, false
	}
	rest := line[i+len("cscope: "):]
	// The wording is "N lines", and "0 lines" for no matches.
	f := strings.Fields(rest)
	if len(f) < 2 || !strings.HasPrefix(f[1], "line") {
		return 0, false
	}
	n, err := strconv.Atoi(f[0])
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// parseResult parses "file function lineno text". cscope cannot represent
// filenames containing spaces, so splitting on the first three fields is
// unambiguous.
func parseResult(line string) (Result, bool) {
	file, rest, ok := strings.Cut(line, " ")
	if !ok {
		return Result{}, false
	}
	fn, rest, ok := strings.Cut(rest, " ")
	if !ok {
		return Result{}, false
	}
	numStr, text, ok := strings.Cut(rest, " ")
	if !ok {
		numStr, text = rest, ""
	}
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return Result{}, false
	}
	return Result{File: file, Function: fn, Line: n, Text: text}, true
}

// Query runs a search and returns its results.
//
// The query is handed to the goroutine owning the subprocess and awaited with
// both the caller's context and a hard timeout. If the caller gives up, or the
// timeout passes, the subprocess is killed and the next query starts a fresh
// one: cscope handles one request at a time, so an abandoned scan would
// otherwise hold up everything behind it.
func (d *DB) Query(ctx context.Context, q Query, term string) ([]Result, error) {
	d.mu.Lock()
	closed := d.closed
	timeout := d.Timeout
	d.mu.Unlock()
	if closed {
		return nil, errors.New("cscope: database is closed")
	}
	if strings.TrimSpace(term) == "" {
		return nil, errors.New("cscope: empty search term")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req := &request{ctx: ctx, query: q, term: term, reply: make(chan response, 1)}
	select {
	case d.reqs <- req:
	case <-d.done:
		return nil, errors.New("cscope: database is closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case res := <-req.reply:
		if res.err != nil {
			return nil, res.err
		}
		return d.resolve(res.results), nil
	case <-d.done:
		return nil, errors.New("cscope: database is closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// resolve rewrites result paths to absolute form.
func (d *DB) resolve(in []Result) []Result {
	for i := range in {
		if !filepath.IsAbs(in[i].File) {
			in[i].File = filepath.Join(d.dir, in[i].File)
		}
	}
	return in
}

// Close shuts down the subprocess.
func (d *DB) Close() error {
	d.closeOnce.Do(func() { close(d.done) })

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	stdin, cmd := d.stdin, d.cmd
	var r *reaper
	if cmd != nil {
		r = d.reaperLocked()
	}
	d.mu.Unlock()

	if stdin != nil {
		// cscope's line interface exits on this.
		fmt.Fprintln(stdin, "q")
		stdin.Close()
	}
	if cmd != nil {
		// Give cscope a moment to exit on its own, then take the whole process
		// group down. Every wait is bounded: quitting the editor must never be
		// held up by a subprocess that has stopped responding, and cmd.Wait can
		// block indefinitely while any descendant still holds the output pipe.
		if !r.wait(300 * time.Millisecond) {
			proc.KillTree(cmd)
			r.wait(300 * time.Millisecond)
		}
	}
	return nil
}

// reaper collects a child process exactly once. exec.Cmd.Wait must not be
// called concurrently on the same Cmd, and this DB has more than one path that
// wants the process gone — Close from the UI and restart from the goroutine
// serving queries, which can both be running when cscope wedges — so they
// share a single Wait instead of each starting their own.
type reaper struct{ done chan struct{} }

// wait blocks until the process has been collected or d elapses, and reports
// which. It may be called repeatedly and from several goroutines.
func (r *reaper) wait(d time.Duration) bool {
	select {
	case <-r.done:
		return true
	case <-time.After(d):
		return false
	}
}

// reaperLocked returns the reaper for the current process, starting its Wait
// on first use. The caller holds d.mu and d.cmd is non-nil.
//
// It is started lazily rather than when cscope launches: Wait closes the
// stdout pipe once the process exits, so calling it early could discard output
// still waiting to be read.
func (d *DB) reaperLocked() *reaper {
	if d.reaper == nil {
		r := &reaper{done: make(chan struct{})}
		cmd := d.cmd
		go func() {
			_ = cmd.Wait()
			close(r.done)
		}()
		d.reaper = r
	}
	return d.reaper
}
