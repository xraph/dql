package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"

	"github.com/xraph/langserver"
	"github.com/xraph/langserver/stdio"
)

// These drive the server the way an editor does — framed messages in, framed
// messages out — rather than calling handlers directly. A handler that works
// in isolation but is wired to the wrong method name, or answers with a shape
// the protocol does not expect, passes a unit test and fails in an editor.

type conversation struct {
	t    *testing.T
	out  *bytes.Buffer
	srv  *server
	next int
}

func newConversation(t *testing.T) *conversation {
	t.Helper()
	return &conversation{t: t, out: &bytes.Buffer{}, next: 1}
}

// send runs one batch of messages through a fresh Serve loop and returns every
// frame the server wrote. Serve reads to EOF, so each call is one exchange.
func (c *conversation) send(bodies ...string) []map[string]any {
	c.t.Helper()

	var in bytes.Buffer
	for _, b := range bodies {
		if err := stdio.WriteMessage(&in, []byte(b)); err != nil {
			c.t.Fatalf("framing request: %v", err)
		}
	}

	if c.srv == nil {
		c.srv = &server{docs: newDocs()}
	}
	c.srv.conn = stdio.NewConn(&in, c.out, langserver.NewBaseSession("test", ""))
	if err := c.srv.conn.Serve(c.srv.methods()); err != nil {
		c.t.Fatalf("Serve: %v", err)
	}

	var msgs []map[string]any
	r := bufio.NewReader(c.out)
	for {
		body, err := stdio.ReadMessage(r)
		if err != nil {
			break
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			c.t.Fatalf("server wrote invalid JSON: %v", err)
		}
		msgs = append(msgs, m)
	}
	c.out.Reset()
	return msgs
}

func req(id int, method, params string) string {
	if params == "" {
		params = "null"
	}
	return `{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"` + method + `","params":` + params + `}`
}

func note(method, params string) string {
	return `{"jsonrpc":"2.0","method":"` + method + `","params":` + params + `}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
