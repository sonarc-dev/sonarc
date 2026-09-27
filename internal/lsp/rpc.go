// Package lsp talks to language servers, such as gopls or clangd, so they can
// answer "where is this defined" about the exact symbol under the cursor.
//
// It is deliberately small: the handful of requests navigation needs, over
// JSON-RPC on the server's stdin and stdout, with only the standard library.
// A server is started the first time a file in its language is asked about,
// and only if it is installed; without one, sonarc works exactly as it does
// with cscope, ctags and its own indexer.
package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
)

// message is any JSON-RPC message: a request (ID and Method), a notification
// (Method only) or a response (ID with Result or Error).
type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s (code %d)", e.Message, e.Code) }

// maxMessage bounds one message from a server. A references answer across a
// large tree is a few megabytes; anything past this is a broken stream.
const maxMessage = 64 << 20

// readMessage reads one Content-Length framed message.
func readMessage(r *bufio.Reader) (*message, error) {
	tp := textproto.NewReader(r)
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(hdr.Get("Content-Length")))
	if err != nil || n < 0 || n > maxMessage {
		return nil, fmt.Errorf("bad Content-Length %q", hdr.Get("Content-Length"))
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("bad message: %w", err)
	}
	return &m, nil
}

// writeMessage frames and writes one message.
func writeMessage(w io.Writer, m any) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// errNotStarted is returned when a server is not running and cannot be.
var errNotStarted = errors.New("language server not running")
