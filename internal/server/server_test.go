package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tranbaohieu/lawliet/internal/resp"
)

// startServer runs a server on a random local port and stops it at test end.
func startServer(t *testing.T) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := New(ln.Addr().String(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop within 5s")
		}
	})
	return srv
}

type client struct {
	t  *testing.T
	nc net.Conn
	r  *resp.Reader
}

func dial(t *testing.T, srv *Server) *client {
	t.Helper()
	nc, err := net.Dial("tcp", srv.addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nc.Close() })
	nc.SetDeadline(time.Now().Add(5 * time.Second))
	return &client{t: t, nc: nc, r: resp.NewReader(nc)}
}

func (c *client) send(args ...string) {
	c.t.Helper()
	var b strings.Builder
	b.WriteString("*" + strconv.Itoa(len(args)) + "\r\n")
	for _, a := range args {
		b.WriteString("$" + strconv.Itoa(len(a)) + "\r\n" + a + "\r\n")
	}
	if _, err := c.nc.Write([]byte(b.String())); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) read() resp.Value {
	c.t.Helper()
	v, err := c.r.ReadValue()
	if err != nil {
		c.t.Fatalf("read reply: %v", err)
	}
	return v
}

func (c *client) do(args ...string) resp.Value {
	c.t.Helper()
	c.send(args...)
	return c.read()
}

func wantSimple(t *testing.T, v resp.Value, s string) {
	t.Helper()
	if v.Type != '+' || v.Str != s {
		t.Fatalf("got %+v, want +%s", v, s)
	}
}

func wantBulk(t *testing.T, v resp.Value, s string) {
	t.Helper()
	if v.Type != '$' || v.Null || v.Str != s {
		t.Fatalf("got %+v, want $%q", v, s)
	}
}

func wantErrPrefix(t *testing.T, v resp.Value, prefix string) {
	t.Helper()
	if v.Type != '-' || !strings.HasPrefix(v.Str, prefix) {
		t.Fatalf("got %+v, want error starting with %q", v, prefix)
	}
}

func TestPingEcho(t *testing.T) {
	c := dial(t, startServer(t))
	wantSimple(t, c.do("PING"), "PONG")
	wantBulk(t, c.do("ping", "hi"), "hi")
	wantErrPrefix(t, c.do("PING", "a", "b"), "ERR wrong number of arguments for 'ping' command")
	wantBulk(t, c.do("ECHO", "a\r\nb"), "a\r\nb")
	wantErrPrefix(t, c.do("ECHO"), "ERR wrong number of arguments for 'echo' command")
}

func TestUnknownCommand(t *testing.T) {
	c := dial(t, startServer(t))
	wantErrPrefix(t, c.do("NOPE", "x"), "ERR unknown command 'nope'")
	// Connection stays usable after an error reply.
	wantSimple(t, c.do("PING"), "PONG")
}

func TestSelect(t *testing.T) {
	c := dial(t, startServer(t))
	wantSimple(t, c.do("SELECT", "0"), "OK")
	wantErrPrefix(t, c.do("SELECT", "1"), "ERR DB index is out of range")
	wantErrPrefix(t, c.do("SELECT", "x"), "ERR value is not an integer")
}

func TestHelloRESP2(t *testing.T) {
	c := dial(t, startServer(t))
	v := c.do("HELLO", "2", "SETNAME", "app1")
	m := v.Map()
	if v.Type != '*' || len(v.Array) != 14 || m["server"].Str != "lawliet" || m["proto"].Int != 2 {
		t.Fatalf("unexpected HELLO reply: %+v", v)
	}
	wantBulk(t, c.do("CLIENT", "GETNAME"), "app1")
	wantErrPrefix(t, c.do("HELLO", "4"), "NOPROTO")
	wantErrPrefix(t, c.do("HELLO", "x"), "ERR Protocol version")
	wantErrPrefix(t, c.do("HELLO", "2", "AUTH", "u", "p"), "ERR AUTH is not supported")
}

func TestHelloRESP3(t *testing.T) {
	c := dial(t, startServer(t))

	// Null before switching: RESP2 encoding.
	if v := c.do("CLIENT", "GETNAME"); v.Type != '$' || !v.Null {
		t.Fatalf("RESP2 null: %+v", v)
	}

	v := c.do("HELLO", "3")
	m := v.Map()
	if v.Type != '%' || len(m) != 7 || m["proto"].Int != 3 || m["server"].Str != "lawliet" {
		t.Fatalf("unexpected HELLO 3 reply: %+v", v)
	}

	// After switching: RESP3 null.
	if v := c.do("CLIENT", "GETNAME"); v.Type != '_' || !v.Null {
		t.Fatalf("RESP3 null: %+v", v)
	}

	// HELLO without version keeps RESP3; invalid HELLO does not change protocol.
	if v := c.do("HELLO"); v.Type != '%' || v.Map()["proto"].Int != 3 {
		t.Fatalf("HELLO (no version): %+v", v)
	}
	wantErrPrefix(t, c.do("HELLO", "2", "SETNAME", "bad name"), "ERR Client names")
	if v := c.do("HELLO"); v.Type != '%' {
		t.Fatalf("protocol changed after failed HELLO: %+v", v)
	}

	// Switching back to RESP2.
	if v := c.do("HELLO", "2"); v.Type != '*' || v.Map()["proto"].Int != 2 {
		t.Fatalf("HELLO 2: %+v", v)
	}
}

func TestClient(t *testing.T) {
	c := dial(t, startServer(t))
	if v := c.do("CLIENT", "GETNAME"); v.Type != '$' || !v.Null {
		t.Fatalf("want null name, got %+v", v)
	}
	wantSimple(t, c.do("CLIENT", "SETNAME", "worker-1"), "OK")
	wantBulk(t, c.do("CLIENT", "GETNAME"), "worker-1")
	wantErrPrefix(t, c.do("CLIENT", "SETNAME", "bad name"), "ERR Client names cannot contain spaces")
	wantSimple(t, c.do("CLIENT", "SETINFO", "LIB-NAME", "redis-py"), "OK")
	if v := c.do("CLIENT", "ID"); v.Type != ':' || v.Int <= 0 {
		t.Fatalf("CLIENT ID: %+v", v)
	}
	wantErrPrefix(t, c.do("CLIENT", "KILL"), "ERR unknown subcommand 'kill'")
}

func TestCommandAndInfo(t *testing.T) {
	c := dial(t, startServer(t))
	if v := c.do("COMMAND", "DOCS"); v.Type != '*' || len(v.Array) != 0 {
		t.Fatalf("COMMAND DOCS: %+v", v)
	}
	if v := c.do("COMMAND", "COUNT"); v.Type != ':' || v.Int < 8 {
		t.Fatalf("COMMAND COUNT: %+v", v)
	}
	info := c.do("INFO")
	for _, want := range []string{"# Server", "lawliet_version:", "# Clients", "connected_clients:1", "# Memory", "# Stats"} {
		if !strings.Contains(info.Str, want) {
			t.Errorf("INFO missing %q:\n%s", want, info.Str)
		}
	}
	server := c.do("INFO", "server")
	if strings.Contains(server.Str, "# Clients") || !strings.Contains(server.Str, "# Server") {
		t.Errorf("INFO server returned wrong sections:\n%s", server.Str)
	}
}

func TestPipeline(t *testing.T) {
	c := dial(t, startServer(t))
	const n = 1000
	var b strings.Builder
	for range n {
		b.WriteString("*1\r\n$4\r\nPING\r\n")
	}
	if _, err := c.nc.Write([]byte(b.String())); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		v := c.read()
		if v.Type != '+' || v.Str != "PONG" {
			t.Fatalf("reply %d: %+v", i, v)
		}
	}
}

func TestInlineCommand(t *testing.T) {
	c := dial(t, startServer(t))
	if _, err := c.nc.Write([]byte("PING\r\nECHO hello\n")); err != nil {
		t.Fatal(err)
	}
	wantSimple(t, c.read(), "PONG")
	wantBulk(t, c.read(), "hello")
}

func TestQuitClosesConnection(t *testing.T) {
	c := dial(t, startServer(t))
	wantSimple(t, c.do("QUIT"), "OK")
	if _, err := c.r.ReadValue(); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF after QUIT, got %v", err)
	}
}

func TestProtocolErrorClosesConnection(t *testing.T) {
	c := dial(t, startServer(t))
	if _, err := c.nc.Write([]byte("*1\r\n+PING\r\n")); err != nil {
		t.Fatal(err)
	}
	wantErrPrefix(t, c.read(), "ERR Protocol error: expected '$'")
	if _, err := c.r.ReadValue(); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF after protocol error, got %v", err)
	}
}

func TestShutdownClosesClients(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := New(ln.Addr().String(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()

	c := dial(t, srv)
	wantSimple(t, c.do("PING"), "PONG")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
	if _, err := c.r.ReadValue(); err == nil {
		t.Fatal("expected client connection to be closed")
	}
}
