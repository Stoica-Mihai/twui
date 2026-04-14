package stream

import (
	"io"
	"sync"
)

type FilteredStream struct {
	reader io.ReadCloser
	mu     sync.Mutex
	cond   *sync.Cond
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
	f.mu.Lock()
	for f.paused && !f.closed {
		f.cond.Wait()
	}
	if f.closed {
		f.mu.Unlock()
		return 0, io.EOF
	}
	f.mu.Unlock()
	n, err := f.reader.Read(p)
	if err != nil {
		f.mu.Lock()
		closed := f.closed
		f.mu.Unlock()
		if closed {
			return n, io.EOF
		}
	}
	return n, err
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
