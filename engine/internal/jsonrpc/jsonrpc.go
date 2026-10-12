// Package jsonrpc reads and writes JSON-RPC 2.0 messages framed with
// Content-Length headers, as the Language Server Protocol sends them.
package jsonrpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// maxMessage is the largest message body Read accepts, in bytes. A larger
// Content-Length is refused before anything is allocated.
const maxMessage = 64 << 20

// maxHeaderLine is the longest header line Read accepts, in bytes, including
// the line ending. A longer line is refused without reading the rest of it.
const maxHeaderLine = 4096

// Standard JSON-RPC and LSP error codes.
const (
	// CodeInvalidRequest means the request is not valid in the server's state.
	CodeInvalidRequest = -32600
	// CodeMethodNotFound means the server has no such method.
	CodeMethodNotFound = -32601
	// CodeInvalidParams means the parameters do not fit the method.
	CodeInvalidParams = -32602
	// CodeNotInitialized means a request came before initialize.
	CodeNotInitialized = -32002
)

// Error is the error of a response.
type Error struct {
	// Code is one of the Code constants, or another the sender chose.
	Code int `json:"code"`
	// Message says what went wrong.
	Message string `json:"message"`
}

// Message is a request, a notification or a response, as received.
type Message struct {
	// ID is the request's id as JSON; it is empty for a notification.
	ID json.RawMessage `json:"id,omitempty"`
	// Method is the name of the method called.
	Method string `json:"method,omitempty"`
	// Params is the method's parameters as JSON, possibly empty.
	Params json.RawMessage `json:"params,omitempty"`
	// Result is the result of a successful response as JSON.
	Result json.RawMessage `json:"result,omitempty"`
	// Error is the error of a failed response, nil otherwise.
	Error *Error `json:"error,omitempty"`
}

// IsNotification reports whether m expects no response.
func (m Message) IsNotification() bool { return len(m.ID) == 0 }

// Conn is a message stream. Reading and writing are independent; writes are
// safe to call from several goroutines, reads are not. A Conn needs
// NewConn.
type Conn struct {
	r  *bufio.Reader
	mu sync.Mutex // guards w
	w  io.Writer
}

// NewConn returns a Conn that reads messages from r and writes them to w.
func NewConn(r io.Reader, w io.Writer) *Conn {
	return &Conn{r: bufio.NewReaderSize(r, maxHeaderLine), w: w}
}

// Read returns the next message. It returns io.EOF when the stream ends
// between messages, and another error when a message is cut short or
// malformed.
func (c *Conn) Read() (Message, error) {
	length, err := c.readHeader()
	if err != nil {
		return Message{}, err
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return Message{}, fmt.Errorf("read body of %d bytes: %w", length, err)
	}
	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		return Message{}, fmt.Errorf("decode message: %w", err)
	}
	return m, nil
}

// readHeader reads the header block and returns its Content-Length. It
// returns io.EOF when the stream ends before any header.
func (c *Conn) readHeader() (int, error) {
	value, found := "", false
	for n := 0; ; n++ {
		line, err := c.r.ReadSlice('\n')
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			return 0, fmt.Errorf("read header: line longer than %d bytes", maxHeaderLine)
		case errors.Is(err, io.EOF) && n == 0 && len(line) == 0:
			return 0, io.EOF
		case err != nil:
			return 0, fmt.Errorf("read header: %w", err)
		}
		text := strings.TrimRight(string(line), "\r\n")
		if text == "" {
			break
		}
		if name, v, ok := strings.Cut(text, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			value, found = strings.TrimSpace(v), true
		}
	}
	length, err := strconv.Atoi(value)
	if !found || err != nil || length < 0 || length > maxMessage {
		return 0, fmt.Errorf("read header: invalid Content-Length %q", value)
	}
	return length, nil
}

// Reply answers the request id with result, which is encoded as JSON; a nil
// result is sent as null.
func (c *Conn) Reply(id json.RawMessage, result any) error {
	return c.write(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{"2.0", id, result})
}

// ReplyError answers the request id with an error of the given code and
// message.
func (c *Conn) ReplyError(id json.RawMessage, code int, message string) error {
	return c.write(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   Error           `json:"error"`
	}{"2.0", id, Error{code, message}})
}

// Notify sends the notification method with params, encoded as JSON.
func (c *Conn) Notify(method string, params any) error {
	return c.write(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", method, params})
}

func (c *Conn) write(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if _, err := c.w.Write(body); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	return nil
}
