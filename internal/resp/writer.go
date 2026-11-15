package resp

import (
	"bufio"
	"io"
	"math"
	"strconv"
	"strings"
)

// Writer buffers RESP replies. Write errors are sticky and surface on Flush.
//
// The protocol version (2 or 3) is per connection and switched by HELLO.
// Types that differ between versions (null, map, double) are encoded
// automatically; command handlers do not need to know the version.
type Writer struct {
	bw    *bufio.Writer
	num   []byte
	proto int
}

// NewWriter returns a buffered Writer speaking RESP2.
func NewWriter(w io.Writer) *Writer {
	return &Writer{bw: bufio.NewWriterSize(w, 64<<10), num: make([]byte, 0, 32), proto: 2}
}

// Proto returns the protocol version in use (2 or 3).
func (w *Writer) Proto() int { return w.proto }

// SetProto switches the protocol version. Only 2 and 3 are valid.
func (w *Writer) SetProto(p int) {
	if p != 2 && p != 3 {
		panic("resp: invalid protocol version " + strconv.Itoa(p))
	}
	w.proto = p
}

var lineBreaks = strings.NewReplacer("\r", " ", "\n", " ")

// WriteSimple writes "+s\r\n". CR/LF in s are replaced by spaces.
func (w *Writer) WriteSimple(s string) { w.line('+', lineBreaks.Replace(s)) }

// WriteError writes "-s\r\n". s should start with an error code, e.g. "ERR ...".
func (w *Writer) WriteError(s string) { w.line('-', lineBreaks.Replace(s)) }

// WriteOK writes "+OK\r\n".
func (w *Writer) WriteOK() { w.bw.WriteString("+OK\r\n") }

// WriteInt writes ":n\r\n".
func (w *Writer) WriteInt(n int64) { w.header(':', n) }

// WriteBulk writes a binary-safe bulk string.
func (w *Writer) WriteBulk(b []byte) {
	w.header('$', int64(len(b)))
	w.bw.Write(b)
	w.bw.WriteString("\r\n")
}

// WriteBulkString writes a bulk string from a Go string.
func (w *Writer) WriteBulkString(s string) {
	w.header('$', int64(len(s)))
	w.bw.WriteString(s)
	w.bw.WriteString("\r\n")
}

// WriteNull writes a null value: "$-1" (RESP2) or "_" (RESP3).
func (w *Writer) WriteNull() {
	if w.proto == 3 {
		w.bw.WriteString("_\r\n")
		return
	}
	w.bw.WriteString("$-1\r\n")
}

// WriteNullArray writes a null array: "*-1" (RESP2) or "_" (RESP3).
func (w *Writer) WriteNullArray() {
	if w.proto == 3 {
		w.bw.WriteString("_\r\n")
		return
	}
	w.bw.WriteString("*-1\r\n")
}

// WriteArrayHeader writes "*n\r\n"; the caller then writes n values.
func (w *Writer) WriteArrayHeader(n int) { w.header('*', int64(n)) }

// WriteMapHeader starts a map of n key/value pairs; the caller then writes 2n values.
// RESP3: "%n". RESP2: flat array "*2n" (same as Redis).
func (w *Writer) WriteMapHeader(n int) {
	if w.proto == 3 {
		w.header('%', int64(n))
		return
	}
	w.header('*', int64(2*n))
}

// WriteDouble writes a float64. RESP3: ",<f>" (inf, -inf, nan allowed).
// RESP2: bulk string with the same text.
func (w *Writer) WriteDouble(f float64) { w.writeFloat(f, 64) }

// WriteFloat32 writes a float32 using its shortest decimal form
// (0.9 instead of 0.8999999761581421). Use it for vector scores.
func (w *Writer) WriteFloat32(f float32) { w.writeFloat(float64(f), 32) }

func (w *Writer) writeFloat(f float64, bitSize int) {
	var s string
	switch {
	case math.IsInf(f, 1):
		s = "inf"
	case math.IsInf(f, -1):
		s = "-inf"
	case math.IsNaN(f):
		s = "nan"
	default:
		s = strconv.FormatFloat(f, 'g', -1, bitSize)
	}
	if w.proto == 3 {
		w.line(',', s)
		return
	}
	w.WriteBulkString(s)
}

// Flush sends buffered replies.
func (w *Writer) Flush() error { return w.bw.Flush() }

func (w *Writer) line(prefix byte, s string) {
	w.bw.WriteByte(prefix)
	w.bw.WriteString(s)
	w.bw.WriteString("\r\n")
}

func (w *Writer) header(prefix byte, n int64) {
	w.bw.WriteByte(prefix)
	w.num = strconv.AppendInt(w.num[:0], n, 10)
	w.bw.Write(w.num)
	w.bw.WriteString("\r\n")
}
