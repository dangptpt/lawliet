package resp

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// Writer buffers RESP2 replies. Write errors are sticky and surface on Flush.
type Writer struct {
	bw  *bufio.Writer
	num []byte
}

// NewWriter returns a buffered Writer.
func NewWriter(w io.Writer) *Writer {
	return &Writer{bw: bufio.NewWriterSize(w, 64<<10), num: make([]byte, 0, 24)}
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

// WriteNull writes a null bulk string "$-1\r\n".
func (w *Writer) WriteNull() { w.bw.WriteString("$-1\r\n") }

// WriteNullArray writes a null array "*-1\r\n".
func (w *Writer) WriteNullArray() { w.bw.WriteString("*-1\r\n") }

// WriteArrayHeader writes "*n\r\n"; the caller then writes n values.
func (w *Writer) WriteArrayHeader(n int) { w.header('*', int64(n)) }

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
