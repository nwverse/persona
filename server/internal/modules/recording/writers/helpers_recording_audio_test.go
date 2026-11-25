package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"encoding/binary"
	"github.com/nwverse/persona/server/internal/kb"
	"math"
)

// pcmAt is n samples of audio from sample from, each sample saying where it is (so a read shows what was stored where).
func pcmAt(from, n int64) []byte {
	out := make([]byte, 2*n)
	for k := range n {
		binary.LittleEndian.PutUint16(out[2*k:], uint16((from+k)*7+1))
	}
	return out
}

// secs is t seconds in samples.
func secs(t float64) int64 { return int64(math.Round(t * kb.AudioRate)) }
