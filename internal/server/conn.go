package server

import (
	"errors"
	"io"
	"net"

	"github.com/tranbaohieu/lawliet/internal/resp"
)

// Conn is one client connection. It is used by a single goroutine.
type Conn struct {
	id   int64
	srv  *Server
	nc   net.Conn
	r    *resp.Reader
	w    *resp.Writer
	name string

	closeAfterReply bool
	cmdName         string // lower-case name of the command being executed
}

func (s *Server) newConn(nc net.Conn) *Conn {
	c := &Conn{
		id:  s.nextID.Add(1),
		srv: s,
		nc:  nc,
		r:   resp.NewReader(nc),
		w:   resp.NewWriter(nc),
	}
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	return c
}

// ID returns the unique connection id.
func (c *Conn) ID() int64 { return c.id }

// W returns the reply writer.
func (c *Conn) W() *resp.Writer { return c.w }

// Server returns the owning server.
func (c *Conn) Server() *Server { return c.srv }

// WrongArgs writes the standard arity error for the current command.
func (c *Conn) WrongArgs() {
	c.w.WriteError("ERR wrong number of arguments for '" + c.cmdName + "' command")
}

func (c *Conn) serve() {
	defer c.srv.removeConn(c)
	defer c.nc.Close()

	for {
		args, err := c.r.ReadCommand()
		if err != nil {
			var pe *resp.ProtocolError
			if errors.As(err, &pe) {
				c.w.WriteError("ERR " + pe.Error())
				c.w.Flush()
			} else if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				c.srv.log.Debug("connection read error", "id", c.id, "err", err)
			}
			return
		}

		c.cmdName = lowerASCII(args[0])
		c.srv.dispatch(c, args)

		// Batch replies for pipelined commands: flush only when no more input is buffered.
		if c.r.Buffered() == 0 || c.closeAfterReply {
			if err := c.w.Flush(); err != nil {
				return
			}
		}
		if c.closeAfterReply {
			return
		}
	}
}

func lowerASCII(b []byte) string {
	out := make([]byte, len(b))
	for i, ch := range b {
		if 'A' <= ch && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		out[i] = ch
	}
	return string(out)
}
