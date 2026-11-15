package command

import (
	"math"
	"strconv"

	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/store"
)

// maxTTLMs caps TTLs at about 100 years so absolute times never overflow.
const maxTTLMs = 100 * 365 * 24 * 3600 * 1000

const errInvalidExpire = cmdError("invalid expire time")

// parseExpire parses a positive TTL in the given unit (1000 for seconds, 1 for ms)
// and returns milliseconds.
func parseExpire(arg []byte, unitMs int64) (int64, error) {
	n, err := strconv.ParseInt(string(arg), 10, 64)
	if err != nil || n <= 0 || n > math.MaxInt64/unitMs || n*unitMs > maxTTLMs {
		return 0, errInvalidExpire
	}
	return n * unitMs, nil
}

// VEXPIRE <col> <id> <seconds>
//
// Reply: 1 if the TTL was set, 0 if the record does not exist.
// A non-positive value deletes the record immediately (same as Redis EXPIRE).
func (h *handlers) vexpire(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	sec, err := strconv.ParseInt(string(args[3]), 10, 64)
	if err != nil || sec > maxTTLMs/1000 || sec < -maxTTLMs/1000 {
		writeErr(c, errInvalidExpire)
		return
	}
	if col.Expire(string(args[2]), h.st.Now()+sec*1000) {
		c.W().WriteInt(1)
	} else {
		c.W().WriteInt(0)
	}
}

// VTTL <col> <id>
//
// Reply: remaining seconds (rounded like Redis TTL), -1 if the record has no
// TTL, -2 if it does not exist.
func (h *handlers) vttl(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	ms, state := col.TTL(string(args[2]))
	switch state {
	case store.TTLNotFound:
		c.W().WriteInt(-2)
	case store.TTLNone:
		c.W().WriteInt(-1)
	default:
		c.W().WriteInt((ms + 500) / 1000)
	}
}

// VPERSIST <col> <id>
//
// Reply: 1 if the TTL was removed, 0 if the record does not exist or has no TTL.
func (h *handlers) vpersist(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	if col.Persist(string(args[2])) {
		c.W().WriteInt(1)
	} else {
		c.W().WriteInt(0)
	}
}
