// Package id generates identifiers in Go, so no database extension is needed (E1 design 5.6).
package id

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

// NewV7 returns a UUID version 7 (RFC 9562): a 48-bit millisecond timestamp followed by random bits, so identifiers
// sort by creation time. now and entropy are injected so tests are deterministic.
func NewV7(now time.Time, entropy io.Reader) (string, error) {
	var u [16]byte
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(now.UnixMilli())) // #nosec G115 -- dates before 1970 are not a supported input
	copy(u[:6], ts[2:])                                        // the low 48 bits
	if _, err := io.ReadFull(entropy, u[6:]); err != nil {
		return "", fmt.Errorf("id: read randomness: %w", err)
	}
	u[6] = u[6]&0x0f | 0x70 // version 7
	u[8] = u[8]&0x3f | 0x80 // variant 10
	h := hex.EncodeToString(u[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}
