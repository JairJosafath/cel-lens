package jsonrpc_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

func TestRead(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		in         string
		wantMethod string
		wantNotif  bool
		wantErr    string // substring; "" means success
		wantEOF    bool
	}{
		{name: "request", in: frame(`{"id":1,"method":"hover","params":{"a":1}}`), wantMethod: "hover"},
		{name: "notification", in: frame(`{"jsonrpc":"2.0","method":"x"}`), wantMethod: "x", wantNotif: true},
		{name: "extra header", in: "Content-Type: x\r\n" + frame(`{"method":"ab"}`), wantMethod: "ab", wantNotif: true},
		{name: "empty stream", in: "", wantEOF: true},
		{name: "missing length", in: "Foo: 1\r\n\r\n{}", wantErr: "Content-Length"},
		{name: "huge length", in: "Content-Length: 99999999999\r\n\r\n", wantErr: "Content-Length"},
		{name: "long header line", in: "X: " + strings.Repeat("a", 1<<20) + "\r\n" + frame(`{}`), wantErr: "line longer"},
		{name: "short body", in: "Content-Length: 50\r\n\r\n{}", wantErr: "read body"},
		{name: "bad json", in: "Content-Length: 3\r\n\r\nabc", wantErr: "decode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := jsonrpc.NewConn(strings.NewReader(tt.in), io.Discard).Read()
			switch {
			case tt.wantEOF:
				if !errors.Is(err, io.EOF) {
					t.Fatalf("Read() error = %v, want io.EOF", err)
				}
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Read() error = %v, want it to contain %q", err, tt.wantErr)
				}
			case err != nil:
				t.Fatalf("Read() error = %v", err)
			default:
				if m.Method != tt.wantMethod || m.IsNotification() != tt.wantNotif {
					t.Errorf("Read() = %+v, want method %q, notification %v", m, tt.wantMethod, tt.wantNotif)
				}
			}
		})
	}
}

func TestWrite(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	c := jsonrpc.NewConn(strings.NewReader(""), &out)
	if err := c.Reply(json.RawMessage("7"), nil); err != nil {
		t.Fatal(err)
	}
	if err := c.ReplyError(json.RawMessage(`"a"`), jsonrpc.CodeMethodNotFound, "no"); err != nil {
		t.Fatal(err)
	}
	if err := c.Notify("n", []int{1}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"jsonrpc":"2.0","id":7,"result":null}`,
		`{"jsonrpc":"2.0","id":"a","error":{"code":-32601,"message":"no"}}`,
		`{"jsonrpc":"2.0","method":"n","params":[1]}`,
	}
	rest := out.String()
	for _, w := range want {
		frame := "Content-Length: " + itoa(len(w)) + "\r\n\r\n" + w
		var ok bool
		if rest, ok = strings.CutPrefix(rest, frame); !ok {
			t.Fatalf("output = %q, want it to start with %q", rest, frame)
		}
	}
	if rest != "" {
		t.Errorf("output has extra %q", rest)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// frame returns body with its Content-Length header.
func frame(body string) string { return "Content-Length: " + itoa(len(body)) + "\r\n\r\n" + body }
