// Package servertest starts a real server on a random port and provides a
// minimal RESP client for integration tests.
package servertest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tranbaohieu/lawliet/internal/resp"
	"github.com/tranbaohieu/lawliet/internal/server"
)

// Start runs a server on 127.0.0.1 with a random port. setup registers extra
// commands before serving. The server stops when the test ends.
// It returns the address to dial.
func Start(t testing.TB, setup func(*server.Server)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(ln.Addr().String(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if setup != nil {
		setup(srv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop within 5s")
		}
	})
	return ln.Addr().String()
}

// Client is a synchronous RESP client for tests.
type Client struct {
	t  testing.TB
	nc net.Conn
	r  *resp.Reader
}

// Dial connects to addr. The connection is closed when the test ends.
func Dial(t testing.TB, addr string) *Client {
	t.Helper()
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nc.Close() })
	nc.SetDeadline(time.Now().Add(30 * time.Second))
	return &Client{t: t, nc: nc, r: resp.NewReader(nc)}
}

// Do sends a command and reads one reply. Arguments may be string, []byte,
// int or float64.
func (c *Client) Do(args ...any) resp.Value {
	c.t.Helper()
	var b strings.Builder
	b.WriteString("*" + strconv.Itoa(len(args)) + "\r\n")
	for _, a := range args {
		var s string
		switch v := a.(type) {
		case string:
			s = v
		case []byte:
			s = string(v)
		case int:
			s = strconv.Itoa(v)
		case float64:
			s = strconv.FormatFloat(v, 'g', -1, 64)
		default:
			c.t.Fatalf("servertest: unsupported argument type %T", a)
		}
		b.WriteString("$" + strconv.Itoa(len(s)) + "\r\n" + s + "\r\n")
	}
	if _, err := c.nc.Write([]byte(b.String())); err != nil {
		c.t.Fatal(err)
	}
	v, err := c.r.ReadValue()
	if err != nil {
		c.t.Fatalf("read reply: %v", err)
	}
	return v
}

// Describe formats a reply for error messages.
func Describe(v resp.Value) string {
	switch v.Type {
	case '*', '%':
		parts := make([]string, len(v.Array))
		for i, e := range v.Array {
			parts[i] = Describe(e)
		}
		return string(v.Type) + "[" + strings.Join(parts, " ") + "]"
	case ':':
		return ":" + strconv.FormatInt(v.Int, 10)
	}
	if v.Null {
		return "nil"
	}
	return fmt.Sprintf("%c%q", v.Type, v.Str)
}
