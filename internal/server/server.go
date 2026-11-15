// Package server implements the TCP server, connection loop and command dispatch.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Handler executes one command. args[0] is the command name.
type Handler func(c *Conn, args [][]byte)

type command struct {
	name  string
	arity int // >0: exact arg count (incl. name); <0: at least -arity
	fn    Handler
}

// Stats holds server-wide counters.
type Stats struct {
	ConnectionsReceived atomic.Int64
	CommandsProcessed   atomic.Int64
}

// Server is a RESP2 TCP server.
type Server struct {
	addr     string
	log      *slog.Logger
	started  time.Time
	commands map[string]*command
	stats    Stats
	nextID   atomic.Int64

	mu    sync.Mutex
	ln    net.Listener
	conns map[*Conn]struct{}
	wg    sync.WaitGroup
}

// New creates a server listening on addr once started.
func New(addr string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		addr:     addr,
		log:      logger,
		started:  time.Now(),
		commands: make(map[string]*command),
		conns:    make(map[*Conn]struct{}),
	}
	registerConnCommands(s)
	return s
}

// Register adds a command. Names are case-insensitive. Panics on duplicates (programming error).
func (s *Server) Register(name string, arity int, fn Handler) {
	key := strings.ToLower(name)
	if _, dup := s.commands[key]; dup {
		panic("server: duplicate command " + key)
	}
	s.commands[key] = &command{name: key, arity: arity, fn: fn}
}

// ListenAndServe listens on the configured address and serves until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve accepts connections on ln until ctx is cancelled, then closes all
// connections and waits for their goroutines to finish.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	s.log.Info("lawliet ready to accept connections", "addr", ln.Addr().String())

	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()

	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			// Transient errors (e.g. too many open files): back off and retry.
			s.log.Warn("accept error", "err", err)
			select {
			case <-time.After(50 * time.Millisecond):
				continue
			case <-ctx.Done():
			}
			break
		}
		s.stats.ConnectionsReceived.Add(1)
		c := s.newConn(nc)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			c.serve()
		}()
	}

	s.closeAll()
	s.wg.Wait()
	s.log.Info("server stopped")
	return nil
}

// Addr returns the listening address, or nil before Serve starts.
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Stats returns server counters.
func (s *Server) Stats() *Stats { return &s.stats }

// Uptime returns time since the server was created.
func (s *Server) Uptime() time.Duration { return time.Since(s.started) }

// ConnectedClients returns the number of open client connections.
func (s *Server) ConnectedClients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

func (s *Server) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		c.nc.Close()
	}
}

func (s *Server) removeConn(c *Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

func (s *Server) dispatch(c *Conn, args [][]byte) {
	s.stats.CommandsProcessed.Add(1)
	cmd, ok := s.commands[c.cmdName]
	if !ok {
		c.w.WriteError(fmt.Sprintf("ERR unknown command '%s'", truncate(c.cmdName, 128)))
		return
	}
	if (cmd.arity > 0 && len(args) != cmd.arity) || (cmd.arity < 0 && len(args) < -cmd.arity) {
		c.WrongArgs()
		return
	}
	cmd.fn(c, args)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
