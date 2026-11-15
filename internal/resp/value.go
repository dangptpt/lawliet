package resp

import (
	"io"
)

// Value is a generic RESP2 reply. Used by tests and client-side tooling.
type Value struct {
	Type  byte    // '+', '-', ':', '$', '*'
	Str   string  // simple string, error, or bulk string content
	Int   int64   // integer reply
	Array []Value // array elements
	Null  bool    // null bulk ($-1) or null array (*-1)
}

// ReadValue reads one RESP2 value of any type.
// A clean io.EOF is returned only if the stream ends before the value starts.
func (r *Reader) ReadValue() (Value, error) {
	if _, err := r.br.Peek(1); err != nil {
		return Value{}, err
	}
	return r.readValue()
}

func (r *Reader) readValue() (Value, error) {
	line, err := r.readLine()
	if err != nil {
		return Value{}, err
	}
	if len(line) == 0 {
		return Value{}, protoErr("empty line")
	}
	v := Value{Type: line[0]}
	body := line[1:]
	switch v.Type {
	case '+', '-':
		v.Str = string(body)
	case ':':
		if v.Int, err = parseInt(body); err != nil {
			return Value{}, protoErr("invalid integer")
		}
	case '$':
		n, err := parseInt(body)
		if err != nil || n > MaxBulkLen {
			return Value{}, protoErr("invalid bulk length")
		}
		if n < 0 {
			v.Null = true
			return v, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r.br, buf); err != nil {
			return Value{}, unexpectedEOF(err)
		}
		v.Str = string(buf[:n])
	case '*':
		n, err := parseInt(body)
		if err != nil || n > MaxArrayLen {
			return Value{}, protoErr("invalid array length")
		}
		if n < 0 {
			v.Null = true
			return v, nil
		}
		v.Array = make([]Value, n)
		for i := range v.Array {
			if v.Array[i], err = r.readValue(); err != nil {
				return Value{}, err
			}
		}
	default:
		return Value{}, protoErr("unknown type '" + string(v.Type) + "'")
	}
	return v, nil
}
