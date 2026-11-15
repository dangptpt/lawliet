package resp

import (
	"bytes"
	"testing"
)

func TestWriter(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	w.WriteOK()
	w.WriteSimple("PONG")
	w.WriteError("ERR line\r\nbreak")
	w.WriteInt(-7)
	w.WriteBulk([]byte("a\r\nb"))
	w.WriteBulkString("")
	w.WriteNull()
	w.WriteNullArray()
	w.WriteArrayHeader(2)
	w.WriteInt(1)
	w.WriteBulkString("x")
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	want := "+OK\r\n" +
		"+PONG\r\n" +
		"-ERR line  break\r\n" +
		":-7\r\n" +
		"$4\r\na\r\nb\r\n" +
		"$0\r\n\r\n" +
		"$-1\r\n" +
		"*-1\r\n" +
		"*2\r\n:1\r\n$1\r\nx\r\n"
	if got := buf.String(); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
