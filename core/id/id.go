// Package id provides sortable, collision-resistant identifiers.
//
// IDs are 26 characters of Crockford base32 encoding a 16-byte value:
// a 48-bit millisecond timestamp followed by 80 bits of randomness.
// They sort lexicographically by creation time, which keeps database
// indexes and change feeds friendly.
package id

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// New returns a new sortable identifier.
func New() string {
	var b [16]byte
	ms := uint64(time.Now().UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		// crypto/rand failing is not a recoverable condition in practice.
		panic("id: cannot read random bytes: " + err.Error())
	}
	return Encode(b[:])
}

// NewShort returns a short random identifier, useful for ephemeral values.
func NewShort() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("id: cannot read random bytes: " + err.Error())
	}
	return Encode(b[:])
}

// Encode converts bytes into Crockford base32 (no padding).
func Encode(src []byte) string {
	// Each 5-byte group becomes 8 characters.
	out := make([]byte, 0, (len(src)*8+4)/5)
	var buf uint64
	var bits uint
	for _, c := range src {
		buf = buf<<8 | uint64(c)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out = append(out, alphabet[(buf>>bits)&31])
		}
	}
	if bits > 0 {
		out = append(out, alphabet[(buf<<(5-bits))&31])
	}
	return string(out)
}

// Timestamp extracts the creation time from an ID created by New.
// It returns the zero time for IDs that do not decode.
func Timestamp(s string) time.Time {
	if len(s) < 10 {
		return time.Time{}
	}
	var buf uint64
	var bits uint
	var out [6]byte
	n := 0
	for i := 0; i < len(s) && n < 6; i++ {
		v := decodeChar(s[i])
		if v < 0 {
			return time.Time{}
		}
		buf = buf<<5 | uint64(v)
		bits += 5
		if bits >= 8 {
			bits -= 8
			out[n] = byte(buf >> bits)
			n++
		}
	}
	if n < 6 {
		return time.Time{}
	}
	ms := int64(binary.BigEndian.Uint64(append([]byte{0, 0}, out[:]...)))
	return time.UnixMilli(ms)
}

func decodeChar(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'A' && c <= 'H':
		return int(c-'A') + 10
	case c >= 'J' && c <= 'K':
		return int(c-'J') + 18
	case c >= 'M' && c <= 'N':
		return int(c-'M') + 20
	case c >= 'P' && c <= 'T':
		return int(c-'P') + 22
	case c >= 'V' && c <= 'Z':
		return int(c-'V') + 27
	}
	return -1
}
