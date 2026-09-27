package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sonarc-dev/sonarc/internal/proc"
)

// client is one running language server.
type client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex // one message at a time on stdin

	mu      sync.Mutex
	nextID  int
	pending map[string]chan *message
	closed  bool
	err     error // why the connection ended

	// utf8 is whether positions are byte offsets, as sonarc's are, rather
	// than the protocol's default of UTF-16 code units.
	utf8 bool

	docsMu sync.Mutex
	docs   map[string]openDoc // what the server has been told about each file
}

type openDoc struct {
	bufVersion uint64 // the editor's version of the text last sent
	lspVersion int    // the protocol's version number for it
}

// start runs the server and completes the initialize handshake.
func start(ctx context.Context, argv []string, root string) (*client, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	proc.SetGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil // servers log copiously; none of it belongs on the terminal
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &client{cmd: cmd, stdin: stdin, pending: map[string]chan *message{}, docs: map[string]openDoc{}}
	go c.read(bufio.NewReaderSize(stdout, 1<<16))

	uri := fileURI(root)
	var res struct {
		Capabilities struct {
			PositionEncoding string `json:"positionEncoding"`
		} `json:"capabilities"`
	}
	err = c.call(ctx, "initialize", map[string]any{
		"processId":        os.Getpid(),
		"clientInfo":       map[string]string{"name": "sonarc"},
		"rootUri":          uri,
		"workspaceFolders": []map[string]string{{"uri": uri, "name": filepath.Base(root)}},
		"capabilities": map[string]any{
			"general": map[string]any{"positionEncodings": []string{"utf-8", "utf-16"}},
			"textDocument": map[string]any{
				"synchronization": map[string]any{"didSave": false},
				"definition":      map[string]any{"linkSupport": true},
				"references":      map[string]any{},
				"hover":           map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
			},
			"workspace": map[string]any{"workspaceFolders": true, "configuration": true},
			"window":    map[string]any{"workDoneProgress": false},
		},
	}, &res)
	if err != nil {
		c.kill()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	c.utf8 = res.Capabilities.PositionEncoding == "utf-8"
	if err := c.notify("initialized", map[string]any{}); err != nil {
		c.kill()
		return nil, err
	}
	return c, nil
}

// read dispatches everything the server sends until the stream ends.
func (c *client) read(r *bufio.Reader) {
	for {
		m, err := readMessage(r)
		if err != nil {
			c.fail(err)
			return
		}
		switch {
		case m.ID != nil && m.Method != "":
			c.answer(m) // the server asking us something
		case m.ID != nil:
			c.mu.Lock()
			ch := c.pending[string(*m.ID)]
			delete(c.pending, string(*m.ID))
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
		// Notifications (diagnostics, log messages, progress) are not used.
	}
}

// answer replies to a request from the server. Servers block on some of these
// (configuration, progress tokens), so each gets the smallest valid reply.
func (c *client) answer(m *message) {
	var result any
	switch m.Method {
	case "workspace/configuration":
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(m.Params, &p)
		result = make([]any, len(p.Items))
	case "window/workDoneProgress/create", "client/registerCapability",
		"client/unregisterCapability", "window/showMessageRequest":
		result = nil
	case "workspace/workspaceFolders":
		result = []any{}
	default:
		c.send(map[string]any{"jsonrpc": "2.0", "id": m.ID,
			"error": rpcError{Code: -32601, Message: "method not supported: " + m.Method}})
		return
	}
	c.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
}

func (c *client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if errors.Is(err, io.EOF) {
		err = errors.New("the language server exited")
	}
	c.err = err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

func (c *client) send(m any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeMessage(c.stdin, m)
}

func (c *client) notify(method string, params any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// call sends a request and waits for its answer, cancelling it on the server
// when ctx ends first.
func (c *client) call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.closed {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.nextID++
	id := c.nextID
	key := fmt.Sprint(id)
	ch := make(chan *message, 1)
	c.pending[key] = ch
	c.mu.Unlock()

	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.fail(err)
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.err
		}
		if m.Error != nil {
			return m.Error
		}
		if result == nil || len(m.Result) == 0 || string(m.Result) == "null" {
			return nil
		}
		return json.Unmarshal(m.Result, result)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		_ = c.notify("$/cancelRequest", map[string]any{"id": id})
		return ctx.Err()
	}
}

// alive reports whether the connection is still up.
func (c *client) alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed
}

// sync tells the server what the file looks like in the editor now, opening
// it the first time. Sending the whole text is simple and, for the files a
// person has open, cheap.
func (c *client) sync(path, languageID string, version uint64, text []byte) error {
	c.docsMu.Lock()
	defer c.docsMu.Unlock()
	d, open := c.docs[path]
	if open && d.bufVersion == version {
		return nil
	}
	uri := fileURI(path)
	if !open {
		d = openDoc{bufVersion: version, lspVersion: 1}
		c.docs[path] = d
		return c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": languageID, "version": d.lspVersion, "text": string(text),
		}})
	}
	d.bufVersion, d.lspVersion = version, d.lspVersion+1
	c.docs[path] = d
	return c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": d.lspVersion},
		"contentChanges": []map[string]any{{"text": string(text)}},
	})
}

// shutdown asks the server to exit and makes sure it does.
func (c *client) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c.alive() {
		_ = c.call(ctx, "shutdown", nil, nil)
		_ = c.notify("exit", nil)
	}
	done := make(chan struct{})
	go func() { _ = c.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		c.kill()
		<-done
	}
}

func (c *client) kill() {
	proc.KillTree(c.cmd)
	c.fail(errors.New("stopped"))
}

// character converts a byte offset in line to the server's column unit.
func (c *client) character(line []byte, col int) int {
	col = min(max(col, 0), len(line))
	if c.utf8 {
		return col
	}
	return utf16Len(line[:col])
}

// byteCol converts the server's column unit back to a byte offset in line.
func (c *client) byteCol(line []byte, char int) int {
	if c.utf8 {
		return min(max(char, 0), len(line))
	}
	units := 0
	for i := 0; i < len(line); {
		if units >= char {
			return i
		}
		r, size := utf8.DecodeRune(line[i:])
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
		i += size
	}
	return len(line)
}

func utf16Len(b []byte) int {
	n := 0
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
		b = b[size:]
	}
	return n
}

// fileURI is the file:// URI of an absolute path.
func fileURI(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String()
}

// uriPath is the path a file:// URI names, or "" for any other kind.
func uriPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return filepath.FromSlash(u.Path)
}

// lineOf returns line n (0-based) of text.
func lineOf(text []byte, n int) []byte {
	for ; n > 0; n-- {
		i := bytes.IndexByte(text, '\n')
		if i < 0 {
			return nil
		}
		text = text[i+1:]
	}
	if i := bytes.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return bytes.TrimSuffix(text, []byte("\r"))
}
