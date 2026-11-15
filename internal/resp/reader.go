// Package resp implements the RESP2 wire protocol used by Redis clients.
package resp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
)

const (
	// MaxBulkLen is the largest bulk string accepted from a client (same as Redis).
	MaxBulkLen = 512 << 20
	// MaxArrayLen is the largest number of arguments accepted in one command.
	MaxArrayLen = 1 << 20
	// readBufSize also bounds the length of a single header or inline line.
	readBufSize = 64 << 10
)

// ProtocolError reports malformed client input. The connection should be closed after it.
type ProtocolError struct{ Msg string }

func (e *ProtocolError) Error() string { return "Protocol error: " + e.Msg }

func protoErr(msg string) error { return &ProtocolError{Msg: msg} }

// Reader reads RESP commands and values from a stream.
type Reader struct {
	br *bufio.Reader
}

// NewReader returns a Reader with an internal buffer.
func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReaderSize(r, readBufSize)}
}

// Buffered reports how many bytes are already buffered (used to batch replies for pipelines).
func (r *Reader) Buffered() int { return r.br.Buffered() }

// ReadCommand reads one client command: a RESP array of bulk strings,
// or an inline command ("PING\r\n") as sent by telnet/nc.
// Empty commands are skipped. The returned slices are owned by the caller.
func (r *Reader) ReadCommand() ([][]byte, error) {
	for {
		b, err := r.br.Peek(1)
		if err != nil {
			return nil, err
		}
		var args [][]byte
		if b[0] == '*' {
			args, err = r.readMultiBulk()
		} else {
			args, err = r.readInline()
		}
		if err != nil {
			return nil, err
		}
		if len(args) > 0 {
			return args, nil
		}
	}
}

// readLine returns a CRLF-terminated line without the CRLF.
// The slice is only valid until the next read.
func (r *Reader) readLine() ([]byte, error) {
	line, err := r.br.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, protoErr("line too long")
	}
	if err != nil {
		return nil, unexpectedEOF(err)
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, protoErr("expected CRLF")
	}
	return line[:len(line)-2], nil
}

func (r *Reader) readMultiBulk() ([][]byte, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}
	n, err := parseInt(line[1:])
	if err != nil || n > MaxArrayLen {
		return nil, protoErr("invalid multibulk length")
	}
	if n <= 0 {
		return nil, nil
	}
	args := make([][]byte, n)
	for i := range args {
		line, err := r.readLine()
		if err != nil {
			return nil, err
		}
		if len(line) == 0 || line[0] != '$' {
			got := "EOF"
			if len(line) > 0 {
				got = string(line[0])
			}
			return nil, protoErr("expected '$', got '" + got + "'")
		}
		l, err := parseInt(line[1:])
		if err != nil || l < 0 || l > MaxBulkLen {
			return nil, protoErr("invalid bulk length")
		}
		buf := make([]byte, l+2)
		if _, err := io.ReadFull(r.br, buf); err != nil {
			return nil, unexpectedEOF(err)
		}
		if buf[l] != '\r' || buf[l+1] != '\n' {
			return nil, protoErr("expected CRLF after bulk string")
		}
		args[i] = buf[:l:l]
	}
	return args, nil
}

// readInline parses a space-separated command line. Quoting is not supported.
func (r *Reader) readInline() ([][]byte, error) {
	line, err := r.br.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, protoErr("too big inline request")
	}
	if err != nil {
		return nil, unexpectedEOF(err)
	}
	fields := bytes.Fields(line)
	args := make([][]byte, len(fields))
	for i, f := range fields {
		args[i] = bytes.Clone(f)
	}
	return args, nil
}

func parseInt(b []byte) (int64, error) {
	if len(b) == 0 {
		return 0, errors.New("empty integer")
	}
	return strconv.ParseInt(string(b), 10, 64)
}

// unexpectedEOF converts io.EOF hit in the middle of a command/value into
// io.ErrUnexpectedEOF. Callers detect a clean EOF earlier, via Peek.
func unexpectedEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
