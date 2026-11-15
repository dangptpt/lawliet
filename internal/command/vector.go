package command

import (
	"encoding/binary"
	"math"
	"strconv"

	"github.com/tranbaohieu/lawliet/internal/store"
)

// parseVector parses a vector argument starting at args[i]:
//
//	FP32 <blob>                 little-endian float32 bytes
//	VALUES <n> v1 v2 ... vn     decimal text
//
// It returns the vector and the index of the next unparsed argument.
// The vector length is checked against the collection dim by the store.
func parseVector(args [][]byte, i int) ([]float32, int, error) {
	if i >= len(args) {
		return nil, i, cmdError("missing vector: expected FP32 <blob> or VALUES <n> v1..vn")
	}
	switch {
	case isKeyword(args[i], "FP32"):
		if i+1 >= len(args) {
			return nil, i, cmdError("FP32 requires a blob argument")
		}
		v, err := decodeFP32(args[i+1])
		return v, i + 2, err

	case isKeyword(args[i], "VALUES"):
		if i+1 >= len(args) {
			return nil, i, cmdError("VALUES requires a count")
		}
		n, err := parseInt(args[i+1], "VALUES count", 1, store.MaxDim)
		if err != nil {
			return nil, i, err
		}
		if i+2+n > len(args) {
			return nil, i, cmdError("VALUES " + strconv.Itoa(n) + " expects " + strconv.Itoa(n) + " numbers, got " + strconv.Itoa(len(args)-i-2))
		}
		v := make([]float32, n)
		for j := range v {
			f, err := strconv.ParseFloat(string(args[i+2+j]), 32)
			if err != nil {
				return nil, i, cmdError("invalid number in VALUES: '" + string(args[i+2+j]) + "'")
			}
			v[j] = float32(f)
		}
		return v, i + 2 + n, nil
	}
	return nil, i, cmdError("expected FP32 or VALUES, got '" + string(args[i]) + "'")
}

// decodeFP32 decodes little-endian IEEE-754 float32 values (DESIGN.md §4.2).
func decodeFP32(blob []byte) ([]float32, error) {
	if len(blob) == 0 || len(blob)%4 != 0 {
		return nil, cmdError("invalid FP32 blob: length " + strconv.Itoa(len(blob)) + " is not a positive multiple of 4")
	}
	v := make([]float32, len(blob)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return v, nil
}

// encodeFP32 encodes v as little-endian float32 bytes.
func encodeFP32(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(x))
	}
	return b
}
