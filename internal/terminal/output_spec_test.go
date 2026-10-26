// output_spec_test.go checks a session's output path as a spec: what a
// client sees doesn't depend on how the exec stream happened to split the
// output into reads.
//
// The properties, for any output stream cut into chunks at any byte
// boundaries:
//   - history (RingBuffer, replayed on attach): after every write, Bytes
//     is the stream's last min(size, written) bytes, Len its length, and
//     Write reports the whole chunk taken. After Reset it's empty and goes
//     on as a new buffer. So the history is the same for every cut.
//   - live output (outputPump), with the consumer stalled until the last
//     chunk is pushed: the consumer gets whole chunks in the order pushed,
//     always the last one, and every one when the stream fits in maxBytes.
//     Read whole, that is the stream itself when it fits, and a run of its
//     chunks ending at its end otherwise.
//
// The spec can fail: TestSpecCanFail runs the checkers with a ring that
// keeps the head of a chunk larger than itself and with a push that
// drops the newest chunk instead of the oldest once over the cap, and
// requires a counterexample of each.
//
// Trusted base: the stdlib (bytes, slices).
package terminal

import (
	"bytes"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// ring is the part of RingBuffer the history checker uses.
type ring interface {
	Write(p []byte) (int, error)
	Bytes() []byte
	Len() int
	Reset()
}

// cut splits stream into chunks at cuts, which are taken modulo the
// stream's length plus one and sorted, so every cut is a valid one.
func cut(stream []byte, cuts []int) [][]byte {
	at := make([]int, 0, len(cuts)+2)
	at = append(at, 0)
	for _, c := range cuts {
		at = append(at, c%(len(stream)+1))
	}
	at = append(at, len(stream))
	slices.Sort(at[1 : len(at)-1])
	out := make([][]byte, 0, len(at)-1)
	for i := 1; i < len(at); i++ {
		out = append(out, stream[at[i-1]:at[i]])
	}
	return out
}

// tail is the last n bytes of b, or all of it.
func tail(b []byte, n int) []byte {
	return b[len(b)-min(n, len(b)):]
}

// checkHistory writes chunks to newRing(size), a Reset halfway when
// reset, and returns how the ring breaks the history property.
func checkHistory(newRing func(size int) ring, size int, chunks [][]byte, reset bool) error {
	r := newRing(size)
	var written []byte
	for i, c := range chunks {
		if reset && i == len(chunks)/2 {
			r.Reset()
			written = nil
			if r.Len() != 0 || len(r.Bytes()) != 0 {
				return fmt.Errorf("size %d: not empty after Reset: %q", size, r.Bytes())
			}
		}
		n, err := r.Write(c)
		if n != len(c) || err != nil {
			return fmt.Errorf("size %d: Write(%q) = %d, %v", size, c, n, err)
		}
		written = append(written, c...)
		want := tail(written, size)
		if got := r.Bytes(); !bytes.Equal(got, want) {
			return fmt.Errorf("size %d, chunks %q: after %d, Bytes = %q, want %q", size, chunks, i+1, got, want)
		}
		if r.Len() != len(want) {
			return fmt.Errorf("size %d, chunks %q: after %d, Len = %d, want %d", size, chunks, i+1, r.Len(), len(want))
		}
	}
	return nil
}

// checkLive pushes chunks, none empty, with push while nobody reads out,
// then reads out until the last chunk arrives, for up to wait, and returns how that
// breaks the live output property for maxBytes.
func checkLive(push func([]byte), out <-chan []byte, maxBytes int, chunks [][]byte, wait time.Duration) error {
	index := map[*byte]int{}
	total := 0
	for i, c := range chunks {
		index[unsafe.SliceData(c)] = i
		total += len(c)
		push(c)
	}
	last := len(chunks) - 1
	var got []int
	timeout := time.After(wait)
	for len(got) == 0 || got[len(got)-1] != last {
		select {
		case c := <-out:
			i, ok := index[unsafe.SliceData(c)]
			if !ok || len(c) != len(chunks[i]) {
				return fmt.Errorf("got %q, not a chunk pushed", c)
			}
			if len(got) > 0 && i <= got[len(got)-1] {
				return fmt.Errorf("got chunk %d after %d", i, got[len(got)-1])
			}
			got = append(got, i)
		case <-timeout:
			return fmt.Errorf("max %d, %d chunks: last chunk never came, got %v", maxBytes, len(chunks), got)
		}
	}
	if total <= maxBytes && len(got) != len(chunks) {
		return fmt.Errorf("max %d, %d bytes: got chunks %v of %d", maxBytes, total, got, len(chunks))
	}
	return nil
}

// specStream is a stream of n bytes from r, multibyte UTF-8 and escape
// sequences included, which a cut can split.
func specStream(r *rand.Rand, n int) []byte {
	pieces := [][]byte{[]byte("a"), []byte("\r\n"), []byte("ü"), []byte("😀"), []byte("\x1b[31m"), {0}, {0xff}}
	var b []byte
	for len(b) < n {
		b = append(b, pieces[r.IntN(len(pieces))]...)
	}
	return b[:n]
}

// specCuts returns k cuts from r.
func specCuts(r *rand.Rand, k int) []int {
	out := make([]int, k)
	for i := range out {
		out[i] = r.IntN(1 << 16)
	}
	return out
}

func newRealRing(size int) ring { return NewRingBuffer(size) }

type OutputSpecSuite struct {
	suite.Suite
}

func TestOutputSpecSuite(t *testing.T) {
	suite.Run(t, new(OutputSpecSuite))
}

// TestHistorySpec checks every cut of short streams into writes, for
// small sizes, then random cuts of longer ones.
func (s *OutputSpecSuite) TestHistorySpec() {
	stream := []byte("abcdefgh")
	for size := range 6 {
		for n := range len(stream) + 1 {
			// Bit i of mask set: cut after byte i.
			for mask := range 1 << max(n-1, 0) {
				var cuts []int
				for i := range n - 1 {
					if mask&(1<<i) != 0 {
						cuts = append(cuts, i+1)
					}
				}
				for _, reset := range []bool{false, true} {
					require.NoError(s.T(), checkHistory(newRealRing, size, cut(stream[:n], cuts), reset))
				}
			}
		}
	}
	r := rand.New(rand.NewPCG(5, 6))
	for range 300 {
		size := r.IntN(64)
		chunks := cut(specStream(r, r.IntN(256)), specCuts(r, r.IntN(12)))
		require.NoError(s.T(), checkHistory(newRealRing, size, chunks, r.IntN(4) == 0))
	}
}

// TestLiveSpec checks random cuts of streams that fit the cap and ones
// that don't.
func (s *OutputSpecSuite) TestLiveSpec() {
	r := rand.New(rand.NewPCG(7, 8))
	for range 200 {
		maxBytes := 1 + r.IntN(128)
		chunks := slices.DeleteFunc(cut(specStream(r, 1+r.IntN(256)), specCuts(r, r.IntN(2*clientChannelBuffer))), func(c []byte) bool { return len(c) == 0 })
		p := newOutputPump(slog.New(slog.DiscardHandler), "spec", maxBytes)
		err := checkLive(p.push, p.out, maxBytes, chunks, 5*time.Second)
		p.close()
		require.NoError(s.T(), err)
	}
}

// headRing keeps the head of a chunk larger than itself, not the tail.
type headRing struct {
	*RingBuffer
	size int
}

func (h headRing) Write(p []byte) (int, error) {
	if len(p) > h.size {
		_, err := h.RingBuffer.Write(p[:h.size])
		return len(p), err
	}
	return h.RingBuffer.Write(p)
}

// TestSpecCanFail: the checkers find a counterexample for each broken
// implementation.
func (s *OutputSpecSuite) TestSpecCanFail() {
	s.Run("ring keeps the head", func() {
		newRing := func(size int) ring { return headRing{NewRingBuffer(size), size} }
		require.Error(s.T(), checkHistory(newRing, 3, cut([]byte("abcdef"), []int{1}), false))
	})
	s.Run("push drops the newest", func() {
		const maxBytes = 4
		p := newOutputPump(slog.New(slog.DiscardHandler), "spec", maxBytes)
		defer p.close()
		queued := 0
		push := func(b []byte) {
			if queued+len(b) > maxBytes {
				return
			}
			queued += len(b)
			p.push(b)
		}
		require.Error(s.T(), checkLive(push, p.out, maxBytes, cut([]byte("abcdef"), []int{2, 4}), 100*time.Millisecond))
	})
}

// FuzzRingBufferHistory checks the history property for any stream, cuts
// and size. Seeds are in testdata/fuzz/FuzzRingBufferHistory.
func FuzzRingBufferHistory(f *testing.F) {
	f.Fuzz(func(t *testing.T, size uint8, stream, cuts []byte, reset bool) {
		at := make([]int, len(cuts))
		for i, c := range cuts {
			at[i] = int(c)
		}
		require.NoError(t, checkHistory(newRealRing, int(size), cut(stream, at), reset))
	})
}
