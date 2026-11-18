package websession

// ringBudget is how many raw output bytes a pane keeps for scrollback. It is
// the 512 KiB a pane daemon holds by default.
const ringBudget = 512 << 10

// ring is a pane's bounded history of the bytes it printed, oldest first. It
// is a circular buffer over a fixed budget, so output wraps in place and a
// long tour never grows the heap.
type ring struct {
	buf []byte
	n   int // bytes held, at most len(buf)
	w   int // next byte to overwrite
}

// newRing returns a ring that holds at most budget bytes.
func newRing(budget int) *ring {
	return &ring{buf: make([]byte, budget)}
}

// Write appends b, dropping the oldest bytes once the ring is full.
func (r *ring) Write(b []byte) {
	size := len(r.buf)
	if size == 0 {
		return
	}
	if len(b) >= size {
		copy(r.buf, b[len(b)-size:])
		r.n, r.w = size, 0
		return
	}
	n := copy(r.buf[r.w:], b)
	r.w = (r.w + n) % size
	if rest := b[n:]; len(rest) > 0 {
		copy(r.buf, rest)
		r.w = len(rest)
	}
	r.n += len(b)
	if r.n > size {
		r.n = size
	}
}

// Bytes returns the held bytes, oldest first, as a copy a frame can carry.
func (r *ring) Bytes() []byte {
	size := len(r.buf)
	if r.n == 0 || size == 0 {
		return nil
	}
	out := make([]byte, r.n)
	start := (r.w - r.n + size) % size
	if start+r.n <= size {
		copy(out, r.buf[start:start+r.n])
		return out
	}
	n := copy(out, r.buf[start:])
	copy(out[n:], r.buf[:r.n-n])
	return out
}
