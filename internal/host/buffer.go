package host

import "sync"

const DefaultBufferCap = 32768 // 32KB

// ByteBuffer is a byte-capped ring buffer for semantic output.
type ByteBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func NewByteBuffer(cap int) *ByteBuffer {
	return &ByteBuffer{
		buf: make([]byte, 0, cap),
		cap: cap,
	}
}

func (b *ByteBuffer) Write(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, s...)
	if len(b.buf) > b.cap {
		b.buf = b.buf[len(b.buf)-b.cap:]
	}
}

func (b *ByteBuffer) Tail(n int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > len(b.buf) {
		n = len(b.buf)
	}
	if n == 0 {
		return ""
	}
	return string(b.buf[len(b.buf)-n:])
}
