package resp

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func readAll(t *testing.T, input string) ([][]string, error) {
	t.Helper()
	r := NewReader(strings.NewReader(input))
	var out [][]string
	for {
		args, err := r.ReadCommand()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
		cmd := make([]string, len(args))
		for i, a := range args {
			cmd[i] = string(a)
		}
		out = append(out, cmd)
	}
}

func TestReadCommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  [][]string
	}{
		{"multibulk", "*1\r\n$4\r\nPING\r\n", [][]string{{"PING"}}},
		{"multibulk args", "*2\r\n$4\r\nECHO\r\n$5\r\nhello\r\n", [][]string{{"ECHO", "hello"}}},
		{"binary bulk with CRLF inside", "*2\r\n$4\r\nECHO\r\n$4\r\na\r\nb\r\n", [][]string{{"ECHO", "a\r\nb"}}},
		{"empty bulk", "*2\r\n$4\r\nECHO\r\n$0\r\n\r\n", [][]string{{"ECHO", ""}}},
		{"pipeline", "*1\r\n$4\r\nPING\r\n*1\r\n$4\r\nPING\r\n", [][]string{{"PING"}, {"PING"}}},
		{"inline CRLF", "PING\r\n", [][]string{{"PING"}}},
		{"inline LF", "ECHO  hi\n", [][]string{{"ECHO", "hi"}}},
		{"skip empty lines", "\r\n\nPING\r\n", [][]string{{"PING"}}},
		{"skip empty multibulk", "*0\r\n*1\r\n$4\r\nPING\r\n", [][]string{{"PING"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readAll(t, tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d commands %q, want %d %q", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if strings.Join(got[i], "|") != strings.Join(tt.want[i], "|") {
					t.Errorf("cmd %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestReadCommandErrors(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		protocol bool // expect *ProtocolError (otherwise an I/O error)
	}{
		{"bad multibulk length", "*x\r\n", true},
		{"multibulk too long", "*99999999\r\n", true},
		{"missing dollar", "*1\r\n+PING\r\n", true},
		{"negative bulk", "*1\r\n$-1\r\n", true},
		{"bulk too long", "*1\r\n$999999999999\r\n", true},
		{"missing CRLF after bulk", "*1\r\n$4\r\nPINGxx", true},
		{"header without CR", "*1\n", true},
		{"truncated bulk", "*1\r\n$4\r\nPI", false},
		{"truncated header", "*1\r\n$4", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readAll(t, tt.input)
			if err == nil {
				t.Fatal("expected error")
			}
			var pe *ProtocolError
			if tt.protocol != errors.As(err, &pe) {
				t.Fatalf("protocol error = %v, want %v (err: %v)", !tt.protocol, tt.protocol, err)
			}
			if !tt.protocol && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("want ErrUnexpectedEOF, got %v", err)
			}
		})
	}
}

func TestReadValue(t *testing.T) {
	input := "+OK\r\n-ERR bad\r\n:42\r\n$5\r\nhello\r\n$-1\r\n*2\r\n:1\r\n$1\r\na\r\n*-1\r\n"
	r := NewReader(strings.NewReader(input))
	checks := []func(Value) bool{
		func(v Value) bool { return v.Type == '+' && v.Str == "OK" },
		func(v Value) bool { return v.Type == '-' && v.Str == "ERR bad" },
		func(v Value) bool { return v.Type == ':' && v.Int == 42 },
		func(v Value) bool { return v.Type == '$' && v.Str == "hello" },
		func(v Value) bool { return v.Type == '$' && v.Null },
		func(v Value) bool {
			return v.Type == '*' && len(v.Array) == 2 && v.Array[0].Int == 1 && v.Array[1].Str == "a"
		},
		func(v Value) bool { return v.Type == '*' && v.Null },
	}
	for i, check := range checks {
		v, err := r.ReadValue()
		if err != nil {
			t.Fatalf("value %d: %v", i, err)
		}
		if !check(v) {
			t.Errorf("value %d unexpected: %+v", i, v)
		}
	}
}

func TestReadValueEOF(t *testing.T) {
	r := NewReader(strings.NewReader(""))
	if _, err := r.ReadValue(); !errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("empty stream: want io.EOF, got %v", err)
	}
	r = NewReader(strings.NewReader("*2\r\n:1\r\n"))
	if _, err := r.ReadValue(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated array: want ErrUnexpectedEOF, got %v", err)
	}
}

func TestReadValueRESP3(t *testing.T) {
	r := NewReader(strings.NewReader("_\r\n,1.5\r\n#t\r\n%2\r\n$1\r\na\r\n:1\r\n$1\r\nb\r\n_\r\n"))
	if v, _ := r.ReadValue(); v.Type != '_' || !v.Null {
		t.Errorf("null: %+v", v)
	}
	if v, _ := r.ReadValue(); v.Type != ',' || v.Str != "1.5" {
		t.Errorf("double: %+v", v)
	}
	if v, _ := r.ReadValue(); v.Type != '#' || !v.Bool {
		t.Errorf("bool: %+v", v)
	}
	v, err := r.ReadValue()
	if err != nil {
		t.Fatal(err)
	}
	m := v.Map()
	if v.Type != '%' || len(m) != 2 || m["a"].Int != 1 || !m["b"].Null {
		t.Errorf("map: %+v", v)
	}
}
