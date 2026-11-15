package resp

import (
	"bytes"
	"math"
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

func TestWriterProtoDependentTypes(t *testing.T) {
	write := func(proto int) string {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		w.SetProto(proto)
		w.WriteNull()
		w.WriteNullArray()
		w.WriteMapHeader(1)
		w.WriteBulkString("k")
		w.WriteInt(1)
		w.WriteDouble(0.5)
		w.WriteDouble(math.Inf(-1))
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	want2 := "$-1\r\n*-1\r\n*2\r\n$1\r\nk\r\n:1\r\n$3\r\n0.5\r\n$4\r\n-inf\r\n"
	if got := write(2); got != want2 {
		t.Errorf("RESP2:\n got  %q\n want %q", got, want2)
	}
	want3 := "_\r\n_\r\n%1\r\n$1\r\nk\r\n:1\r\n,0.5\r\n,-inf\r\n"
	if got := write(3); got != want3 {
		t.Errorf("RESP3:\n got  %q\n want %q", got, want3)
	}
}

func TestWriterDefaultProtoIs2(t *testing.T) {
	if p := NewWriter(&bytes.Buffer{}).Proto(); p != 2 {
		t.Fatalf("default proto = %d, want 2", p)
	}
}
