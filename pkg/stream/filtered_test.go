package stream

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"
)

// nopCloser wraps an io.Reader with a no-op Close method.
type nopCloser struct {
	io.Reader
	closed bool
	mu     sync.Mutex
}

func (n *nopCloser) Close() error {
	n.mu.Lock()
	n.closed = true
	n.mu.Unlock()
	return nil
}

func (n *nopCloser) isClosed() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.closed
}

func TestFilteredStream_Read(t *testing.T) {
	data := []byte("hello world")
	rc := &nopCloser{Reader: bytes.NewReader(data)}
	fs := NewFilteredStream(rc)

	buf := make([]byte, 64)
	n, err := fs.Read(buf)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if string(buf[:n]) != "hello world" {
		t.Errorf("Read = %q, want %q", string(buf[:n]), "hello world")
	}
}

func TestFilteredStream_Read_EOF(t *testing.T) {
	data := []byte("short")
	rc := &nopCloser{Reader: bytes.NewReader(data)}
	fs := NewFilteredStream(rc)

	// Read all data.
	buf := make([]byte, 64)
	n, _ := fs.Read(buf)
	if string(buf[:n]) != "short" {
		t.Errorf("first Read = %q, want %q", string(buf[:n]), "short")
	}

	// Next read should return EOF.
	_, err := fs.Read(buf)
	if err != io.EOF {
		t.Errorf("second Read err = %v, want io.EOF", err)
	}
}

func TestFilteredStream_Close(t *testing.T) {
	rc := &nopCloser{Reader: bytes.NewReader([]byte("data"))}
	fs := NewFilteredStream(rc)

	err := fs.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}
	if !rc.isClosed() {
		t.Error("underlying reader should be closed")
	}
}

func TestFilteredStream_ReadAfterClose_ReturnsEOF(t *testing.T) {
	rc := &nopCloser{Reader: bytes.NewReader([]byte("data"))}
	fs := NewFilteredStream(rc)

	fs.Close()

	buf := make([]byte, 64)
	_, err := fs.Read(buf)
	if err != io.EOF {
		t.Errorf("Read after Close err = %v, want io.EOF", err)
	}
}

func TestFilteredStream_PauseResume(t *testing.T) {
	// Use a pipe so reads block until data is written.
	pr, pw := io.Pipe()
	fs := NewFilteredStream(pr)

	fs.Pause()

	// Start a goroutine that tries to read from the paused stream.
	readDone := make(chan struct{})
	var readN int
	var readErr error
	buf := make([]byte, 64)
	go func() {
		readN, readErr = fs.Read(buf)
		close(readDone)
	}()

	// Give the goroutine time to block on the condition variable.
	time.Sleep(50 * time.Millisecond)

	// Confirm it hasn't completed yet.
	select {
	case <-readDone:
		t.Fatal("Read should block while paused")
	default:
	}

	// Write some data and resume.
	go pw.Write([]byte("resumed"))
	fs.Resume()

	// Wait for the read to complete.
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not unblock after Resume")
	}

	if readErr != nil {
		t.Fatalf("Read after resume error: %v", readErr)
	}
	if string(buf[:readN]) != "resumed" {
		t.Errorf("Read = %q, want %q", string(buf[:readN]), "resumed")
	}

	pw.Close()
	fs.Close()
}

func TestFilteredStream_CloseUnblocksPausedRead(t *testing.T) {
	pr, pw := io.Pipe()
	fs := NewFilteredStream(pr)

	fs.Pause()

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		_, err := fs.Read(buf)
		readDone <- err
	}()

	// Let the goroutine block on the condition variable.
	time.Sleep(50 * time.Millisecond)

	// Closing should unblock the paused read.
	fs.Close()

	select {
	case err := <-readDone:
		if err != io.EOF {
			t.Errorf("Read after Close err = %v, want io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read was not unblocked by Close")
	}

	pw.Close()
}

func TestFilteredStream_SwapReader_ContinuesReading(t *testing.T) {
	first := &nopCloser{Reader: bytes.NewReader([]byte("AAA"))}
	second := &nopCloser{Reader: bytes.NewReader([]byte("BBB"))}
	fs := NewFilteredStream(first)

	buf := make([]byte, 3)
	n, err := fs.Read(buf)
	if err != nil || string(buf[:n]) != "AAA" {
		t.Fatalf("first Read = (%q, %v), want (\"AAA\", nil)", buf[:n], err)
	}

	// Swap before the first reader returns EOF on a subsequent Read.
	fs.SwapReader(second)
	if !first.isClosed() {
		t.Error("SwapReader should close the previous reader")
	}

	n, err = fs.Read(buf)
	if err != nil || string(buf[:n]) != "BBB" {
		t.Fatalf("post-swap Read = (%q, %v), want (\"BBB\", nil)", buf[:n], err)
	}
}

func TestFilteredStream_SwapReader_UnblocksPendingRead(t *testing.T) {
	// Reader A blocks forever; the swap should unblock the in-flight Read
	// and let it continue against reader B.
	prA, pwA := io.Pipe()
	fs := NewFilteredStream(prA)

	type result struct {
		data string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		buf := make([]byte, 16)
		n, err := fs.Read(buf)
		done <- result{data: string(buf[:n]), err: err}
	}()

	// Let the goroutine block inside prA.Read.
	time.Sleep(50 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("Read should block on pending pipe")
	default:
	}

	second := &nopCloser{Reader: bytes.NewReader([]byte("swapped"))}
	fs.SwapReader(second)

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Read after swap err = %v, want nil", r.err)
		}
		if r.data != "swapped" {
			t.Errorf("Read after swap = %q, want %q", r.data, "swapped")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not resume on new reader after SwapReader")
	}

	_ = pwA.Close()
	fs.Close()
}

func TestFilteredStream_SwapReader_AfterCloseIsNoop(t *testing.T) {
	first := &nopCloser{Reader: bytes.NewReader([]byte("data"))}
	fs := NewFilteredStream(first)
	fs.Close()

	second := &nopCloser{Reader: bytes.NewReader([]byte("late"))}
	fs.SwapReader(second)

	if !second.isClosed() {
		t.Error("SwapReader on a closed stream should close the incoming reader")
	}

	buf := make([]byte, 16)
	if _, err := fs.Read(buf); err != io.EOF {
		t.Errorf("Read after SwapReader-on-closed err = %v, want io.EOF", err)
	}
}

func TestFilteredStream_PauseIsIdempotent(t *testing.T) {
	rc := &nopCloser{Reader: bytes.NewReader([]byte("data"))}
	fs := NewFilteredStream(rc)

	// Multiple pauses should not panic.
	fs.Pause()
	fs.Pause()
	fs.Resume()
	fs.Resume()

	fs.Close()
}
