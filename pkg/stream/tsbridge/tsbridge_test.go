package tsbridge

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// stagedReader feeds bytes in explicit chunks: the test writes what
// should be read next and signals the reader. Lets us interpose
// MarkSwap between reads without racing on real pipe scheduling.
func stagedPipe() (*io.PipeReader, *io.PipeWriter) { return io.Pipe() }

// --- Low-level codec round-trips ---

func TestReadWriteTimestamp_RoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 90000, 1 << 30, 1<<33 - 1}
	for _, want := range cases {
		var buf [5]byte
		writeTimestamp(buf[:], want, 0x02)
		got := readTimestamp(buf[:])
		if got != want {
			t.Errorf("PTS round-trip failed: wrote %d, read %d", want, got)
		}
		// Marker bits must all be 1.
		if buf[0]&0x01 != 1 || buf[2]&0x01 != 1 || buf[4]&0x01 != 1 {
			t.Errorf("marker bits not set in encoded PTS: % x", buf)
		}
	}
}

func TestReadWritePCR_RoundTrip(t *testing.T) {
	cases := []struct{ base, ext uint64 }{
		{0, 0},
		{1, 1},
		{90000, 150},
		{(1 << 33) - 1, 299},
	}
	for _, c := range cases {
		var buf [6]byte
		writePCR(buf[:], c.base, c.ext)
		gotBase, gotExt := readPCR(buf[:])
		if gotBase != c.base || gotExt != c.ext {
			t.Errorf("PCR round-trip: wrote (%d,%d), read (%d,%d)", c.base, c.ext, gotBase, gotExt)
		}
	}
}

// --- Packet fabrication helpers ---

// tsPacketWithPES builds a 188-byte TS packet carrying a PES start with
// the given PID and PTS. Pads payload with 0xFF.
func tsPacketWithPES(pid uint16, pts uint64) []byte {
	p := make([]byte, tsPacketSize)
	p[0] = tsSyncByte
	p[1] = 0x40 | byte(pid>>8&0x1f) // PUSI=1
	p[2] = byte(pid & 0xff)
	p[3] = 0x10 // AFC=01 (payload only), CC=0
	// PES header at bytes 4..
	p[4] = 0x00
	p[5] = 0x00
	p[6] = 0x01
	p[7] = 0xe0  // video stream_id
	p[8] = 0x00  // length high
	p[9] = 0x00  // length low
	p[10] = 0x80 // '10' marker, no scramble, no align
	p[11] = 0x80 // PTS only (PTS_DTS_flags='10')
	p[12] = 0x05 // header data length: 5 bytes of PTS
	writeTimestamp(p[13:18], pts, 0x02)
	for i := 18; i < tsPacketSize; i++ {
		p[i] = 0xff
	}
	return p
}

// tsPacketWithPCR builds a 188-byte TS packet with an adaptation field
// carrying the given PCR for PID.
func tsPacketWithPCR(pid uint16, base, ext uint64) []byte {
	p := make([]byte, tsPacketSize)
	p[0] = tsSyncByte
	p[1] = byte(pid >> 8 & 0x1f) // PUSI=0
	p[2] = byte(pid & 0xff)
	p[3] = 0x20 // AFC=10 (AF only), CC=0
	p[4] = 0xb7 // AF length = 183 (fills the packet)
	p[5] = 0x10 // PCR flag
	writePCR(p[6:12], base, ext)
	for i := 12; i < tsPacketSize; i++ {
		p[i] = 0xff
	}
	return p
}

// packetPTS returns the PTS embedded in a packet built by tsPacketWithPES.
func packetPTS(p []byte) uint64 {
	return readTimestamp(p[13:18])
}

// packetPCRBase returns the PCR base embedded in a packet built by
// tsPacketWithPCR.
func packetPCRBase(p []byte) uint64 {
	b, _ := readPCR(p[6:12])
	return b
}

// --- Bridge behavior ---

func TestBridge_PassThroughBeforeSwap(t *testing.T) {
	orig := tsPacketWithPES(256, 50000)
	b := New(io.NopCloser(bytes.NewReader(orig)))

	out := make([]byte, tsPacketSize)
	if _, err := io.ReadFull(b, out); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if !bytes.Equal(out, orig) {
		t.Errorf("bytes modified before MarkSwap\nwant: % x\ngot:  % x", orig[:20], out[:20])
	}
	if got := packetPTS(out); got != 50000 {
		t.Errorf("PTS changed before swap: got %d, want 50000", got)
	}
}

func TestBridge_RewritesPTSOnSwap(t *testing.T) {
	// Use io.Pipe so the test can interpose MarkSwap between the
	// pre-swap and post-swap writes.
	pre := tsPacketWithPES(256, 10000)
	post := tsPacketWithPES(256, 100)

	pr, pw := stagedPipe()
	b := New(pr)

	go func() {
		_, _ = pw.Write(pre)
		// Busy loop until the reader has consumed everything — emit is
		// drained and carry is empty. That's the safe moment for the
		// test to MarkSwap before new bytes land.
		waitForQuiet(b, 200*time.Millisecond)
		_, _ = pw.Write(post)
		_ = pw.Close()
	}()

	outPre := make([]byte, tsPacketSize)
	if _, err := io.ReadFull(b, outPre); err != nil {
		t.Fatalf("read pre: %v", err)
	}
	if got := packetPTS(outPre); got != 10000 {
		t.Errorf("pre-swap PTS changed: got %d, want 10000", got)
	}

	b.MarkSwap()

	outPost := make([]byte, tsPacketSize)
	if _, err := io.ReadFull(b, outPost); err != nil {
		t.Fatalf("read post: %v", err)
	}
	want := uint64(10000 + ptsContinuationDelta)
	if got := packetPTS(outPost); got != want {
		t.Errorf("post-swap PTS = %d, want %d (pre + %d)", got, want, ptsContinuationDelta)
	}
}

func TestBridge_ConstantOffsetAcrossSamePID(t *testing.T) {
	pre := tsPacketWithPES(256, 10000)
	post1 := tsPacketWithPES(256, 500)
	post2 := tsPacketWithPES(256, 3500)

	pr, pw := stagedPipe()
	b := New(pr)

	go func() {
		_, _ = pw.Write(pre)
		waitForQuiet(b, 200*time.Millisecond)
		_, _ = pw.Write(post1)
		_, _ = pw.Write(post2)
		_ = pw.Close()
	}()

	buf1 := make([]byte, tsPacketSize)
	if _, err := io.ReadFull(b, buf1); err != nil {
		t.Fatalf("read1: %v", err)
	}
	b.MarkSwap()
	buf2 := make([]byte, tsPacketSize)
	if _, err := io.ReadFull(b, buf2); err != nil {
		t.Fatalf("read2: %v", err)
	}
	buf3 := make([]byte, tsPacketSize)
	if _, err := io.ReadFull(b, buf3); err != nil {
		t.Fatalf("read3: %v", err)
	}

	p2 := packetPTS(buf2)
	p3 := packetPTS(buf3)
	if p3-p2 != 3000 {
		t.Errorf("progression broken across packets on same PID: p2=%d p3=%d delta=%d want 3000", p2, p3, p3-p2)
	}
}

// waitForQuiet busy-waits up to d for the bridge to have no buffered
// carry/emit bytes — i.e. everything written so far has been processed
// and handed to the reader. Test helper only.
func waitForQuiet(b *Bridge, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		quiet := len(b.carry) == 0 && len(b.emit) == 0
		b.mu.Unlock()
		if quiet {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBridge_RewritesPCROnSwap(t *testing.T) {
	pre := tsPacketWithPCR(100, 20000, 0)
	post := tsPacketWithPCR(100, 1, 0)

	pr, pw := stagedPipe()
	b := New(pr)
	go func() {
		_, _ = pw.Write(pre)
		waitForQuiet(b, 200*time.Millisecond)
		_, _ = pw.Write(post)
		_ = pw.Close()
	}()

	pre0 := make([]byte, tsPacketSize)
	if _, err := io.ReadFull(b, pre0); err != nil {
		t.Fatalf("read pre: %v", err)
	}
	b.MarkSwap()
	post0 := make([]byte, tsPacketSize)
	if _, err := io.ReadFull(b, post0); err != nil {
		t.Fatalf("read post: %v", err)
	}

	want := uint64(20000 + ptsContinuationDelta)
	if got := packetPCRBase(post0); got != want {
		t.Errorf("post-swap PCR_base = %d, want %d", got, want)
	}
}

func TestBridge_DifferentPIDsTrackedIndependently(t *testing.T) {
	// Video PID 256 and audio PID 257 each need their own offset.
	preV := tsPacketWithPES(256, 10000)
	preA := tsPacketWithPES(257, 50000)
	postV := tsPacketWithPES(256, 100)
	postA := tsPacketWithPES(257, 700)

	pr, pw := stagedPipe()
	b := New(pr)
	go func() {
		_, _ = pw.Write(preV)
		_, _ = pw.Write(preA)
		waitForQuiet(b, 200*time.Millisecond)
		_, _ = pw.Write(postV)
		_, _ = pw.Write(postA)
		_ = pw.Close()
	}()

	pre1 := make([]byte, tsPacketSize)
	_, _ = io.ReadFull(b, pre1)
	pre2 := make([]byte, tsPacketSize)
	_, _ = io.ReadFull(b, pre2)
	b.MarkSwap()
	post1 := make([]byte, tsPacketSize)
	_, _ = io.ReadFull(b, post1)
	post2 := make([]byte, tsPacketSize)
	_, _ = io.ReadFull(b, post2)

	// Video offset: 256's last PTS was 10000 → post = 10000+3000 = 13000.
	if got := packetPTS(post1); got != 13000 {
		t.Errorf("video post-swap PTS = %d, want 13000", got)
	}
	// Audio offset: 257's last PTS was 50000 → post = 50000+3000 = 53000.
	if got := packetPTS(post2); got != 53000 {
		t.Errorf("audio post-swap PTS = %d, want 53000", got)
	}
}

func TestBridge_UnalignedReadBoundary(t *testing.T) {
	// Verify that when the underlying reader delivers bytes in chunks
	// smaller than a TS packet, the bridge still assembles and emits
	// correctly. No MarkSwap in this test — it's purely about alignment.
	a := tsPacketWithPES(256, 10000)
	bpkt := tsPacketWithPES(256, 13000)
	src := append(append([]byte{}, a...), bpkt...)
	r := &chunkReader{buf: src, chunk: 13}
	br := New(io.NopCloser(r))

	buf1 := make([]byte, tsPacketSize)
	if _, err := io.ReadFull(br, buf1); err != nil {
		t.Fatalf("read1: %v", err)
	}
	buf2 := make([]byte, tsPacketSize)
	if _, err := io.ReadFull(br, buf2); err != nil {
		t.Fatalf("read2: %v", err)
	}
	if got := packetPTS(buf1); got != 10000 {
		t.Errorf("buf1 PTS = %d, want 10000", got)
	}
	if got := packetPTS(buf2); got != 13000 {
		t.Errorf("buf2 PTS = %d, want 13000", got)
	}
}

type chunkReader struct {
	buf   []byte
	chunk int
	pos   int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.buf) {
		return 0, io.EOF
	}
	n := c.chunk
	if n > len(p) {
		n = len(p)
	}
	if c.pos+n > len(c.buf) {
		n = len(c.buf) - c.pos
	}
	copy(p, c.buf[c.pos:c.pos+n])
	c.pos += n
	return n, nil
}

// Silence unused imports when building without all tests active.
var _ = bytes.NewReader
