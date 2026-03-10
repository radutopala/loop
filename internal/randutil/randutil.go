package randutil

import (
	"crypto/rand"
	"encoding/hex"
)

// HexID generates a random hex string of 2*n characters (n random bytes).
func HexID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
