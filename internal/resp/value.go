package resp

import (
	"io"
)

// Value is a generic RESP2/RESP3 reply. Used by tests and client-side tooling.
type Value struct {
	Type  byte    // '+', '-', ':', '$', '*' and RESP3 '_', ',', '#', '%'
	Str   string  // simple string, error, bulk string, or double text
	Int   int64   // integer reply
	Bool  bool    // RESP3 boolean
	Array []Value // array elements; for maps: flattened key, value, key, value...
	Null  bool    // RESP2 null bulk/array, or RESP3 null '_'
}

// Map returns a map reply ('%', or RESP2 flat array) as key -> value.
func (v Value) Map() map[string]Value {
	m := make(map[string]Value, len(v.Array)/2)
	for i := 0; i+1 < len(v.Array); i += 2 {
		m[v.Array[i].Str] = v.Array[i+1]
	}
	return m
}

// ReadValue reads one RESP value of any supported type.
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
	case '+', '-', ',':
		v.Str = string(body)
	case '_':
		v.Null = true
	case '#':
		switch string(body) {
		case "t":
			v.Bool = true
		case "f":
		default:
			return Value{}, protoErr("invalid boolean")
		}
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
	case '*', '%':
		n, err := parseInt(body)
		if err != nil || n > MaxArrayLen {
			return Value{}, protoErr("invalid array length")
		}
		if n < 0 {
			v.Null = true
			return v, nil
		}
		if v.Type == '%' {
			n *= 2
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
