package record

import (
	"crypto/rand"
	"sync"
	"time"
)

// crockford is Crockford's base32 alphabet (no I, L, O, U): 32 symbols whose
// ASCII order matches their numeric value, so a lexical sort of the encoded
// string is a numeric sort of the underlying integer.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	ulidMu      sync.Mutex
	ulidLastMS  uint64
	ulidEntropy [10]byte
)

// newULID returns a 26-character ULID: a 48-bit millisecond timestamp then 80
// bits of entropy, Crockford base32, lexicographically sortable by time. It
// is monotonic within this process — two calls in the same millisecond bump
// the 80-bit entropy big-endian (carrying into higher bytes) rather than
// reroll it — so a burst of checkpoints from one session sorts in the order
// it was written.
func newULID() string {
	ms := uint64(time.Now().UnixMilli())

	ulidMu.Lock()
	if ms <= ulidLastMS {
		ms = ulidLastMS
		for i := len(ulidEntropy) - 1; i >= 0; i-- {
			ulidEntropy[i]++
			if ulidEntropy[i] != 0 {
				break
			}
		}
	} else {
		ulidLastMS = ms
		_, _ = rand.Read(ulidEntropy[:])
	}
	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	copy(b[6:], ulidEntropy[:])
	ulidMu.Unlock()

	return encodeULID(b)
}

// shard is the two-character checkpoint directory: the last two ULID
// characters, which come from the entropy and so spread evenly.
func shard(id string) string {
	return id[len(id)-2:]
}

// encodeULID renders the 16 ULID bytes as 26 Crockford base32 characters,
// most-significant bit first. The value is 128 bits and 26 symbols carry 130,
// so the top two bits are an implicit zero pad (the -2 bit offset below);
// walking bits from the MSB keeps the encoding order-preserving.
func encodeULID(b [16]byte) string {
	out := make([]byte, 26)
	for i := range out {
		v := 0
		for j := range 5 {
			pos := i*5 + j - 2
			v <<= 1
			if pos >= 0 && pos < 128 {
				v |= int(b[pos/8]>>(7-uint(pos%8))) & 1
			}
		}
		out[i] = crockford[v]
	}
	return string(out)
}
