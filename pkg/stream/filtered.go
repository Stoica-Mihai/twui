package stream

import (
	"io"
	"sync"
)

type FilteredStream struct {
	reader io.ReadCloser
	mu     sync.Mutex
	cond   *sync.Cond
	// gen is bumped on every SwapReader so an in-flight Read that sees an
	// error after a concurrent swap can detect it and transparently retry
	// against the new reader instead of propagating the error upstream.
	gen    uint64
	paused bool
	closed bool
}

func NewFilteredStream(reader io.ReadCloser) *FilteredStream {
	f := &FilteredStream{
		reader: reader,
	}
	f.cond = sync.NewCond(&f.mu)
	return f
}

func (f *FilteredStream) Read(p []byte) (int, error) {
	for {
		f.mu.Lock()
		for f.paused && !f.closed {
			f.cond.Wait()
		}
		if f.closed {
			f.mu.Unlock()
			return 0, io.EOF
		}
		r := f.reader
		gen := f.gen
		f.mu.Unlock()

		n, err := r.Read(p)
		if n > 0 || err == nil {
			return n, err
		}

		// (0, err) — check whether this was a SwapReader-induced close
		// rather than a real EOF/error before propagating to the caller.
		f.mu.Lock()
		swapped := f.gen != gen
		closed := f.closed
		f.mu.Unlock()
		if closed {
			return 0, io.EOF
		}
		if swapped {
			continue
		}
		return n, err
	}
}

func (f *FilteredStream) Close() error {
	f.mu.Lock()
	f.closed = true
	err := f.reader.Close()
	f.cond.Broadcast()
	f.mu.Unlock()
	return err
}

func (f *FilteredStream) Pause() {
	f.mu.Lock()
	f.paused = true
	f.mu.Unlock()
}

func (f *FilteredStream) Resume() {
	f.mu.Lock()
	f.paused = false
	f.cond.Broadcast()
	f.mu.Unlock()
}

// SwapReader replaces the underlying reader with r and closes the previous
// one. A concurrent Read call blocked on the old reader will unblock (via
// the close) and transparently retry against the new reader — callers see
// a continuous byte stream across the swap.
//
// If the stream has already been closed, SwapReader is a no-op and the new
// reader is closed immediately.
func (f *FilteredStream) SwapReader(r io.ReadCloser) {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		_ = r.Close()
		return
	}
	old := f.reader
	f.reader = r
	f.gen++
	f.cond.Broadcast()
	f.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}
