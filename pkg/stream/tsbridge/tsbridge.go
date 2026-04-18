// Package tsbridge makes MPEG-TS timestamps continuous across mid-stream
// source swaps — e.g. the ad-break bypass that pivots the player onto a
// fresh Twitch session with its own PTS/PCR base. Wrapping the reader
// the player consumes with a Bridge and calling MarkSwap() before the
// underlying source changes rewrites PCR, PTS, and DTS in outgoing TS
// packets so the player's demuxer sees one uninterrupted timeline.
//
// Scope: Twitch's HLS TS streams, which are well-formed MPEG-2 TS. We
// parse the minimum needed — sync byte, PID, adaptation field, PCR,
// PES start + PTS/DTS — and pass bytes through unchanged when there's
// no active swap adjustment to apply.
package tsbridge

import (
	"io"
	"sync"
)

const (
	tsPacketSize = 188
	tsSyncByte   = 0x47

	// ptsModulo is 2^33 — PTS/DTS are 33-bit values that wrap.
	ptsModulo = 1 << 33
	// pcrBaseModulo is 2^33 — PCR base is also 33 bits at 90kHz.
	pcrBaseModulo = 1 << 33
	// pcrExtModulo is 300 — PCR extension is 9 bits but values run 0..299.
	pcrExtModulo = 300

	// ptsContinuationDelta is a one-frame-ish gap inserted between the
	// last emitted PTS on a PID and the first post-swap PTS rewrite target
	// so the timeline moves forward rather than repeating a timestamp.
	// 3000 ticks @ 90kHz ≈ 33ms ≈ one 30fps frame.
	ptsContinuationDelta = 3000
)

// Bridge is an io.ReadCloser that rewrites MPEG-TS timestamps to stay
// continuous across source swaps signalled via MarkSwap.
type Bridge struct {
	inner io.ReadCloser

	mu sync.Mutex

	// lastPTS records the most recent PTS emitted on each PID. Used at
	// the first post-swap packet to compute the offset that makes the
	// new stream continue from that value.
	lastPTS map[uint16]uint64
	// lastPCRBase / lastPCRExt record the most recent PCR emitted on the
	// PID that carries PCR (there's typically just one, the PMT's PCR
	// PID, but we track per-PID for robustness).
	lastPCRBase map[uint16]uint64
	lastPCRExt  map[uint16]uint64

	// pendingSwap is set by MarkSwap; the next packet seen on each PID
	// computes and clears its offset entry.
	pendingSwap bool

	// ptsOffset applied to PTS/DTS on each PID post-swap. Modular in
	// ptsModulo. Computed on the first PES-start packet after a swap.
	ptsOffset map[uint16]uint64
	// pcrOffsetBase / pcrOffsetExt applied to PCR_base / PCR_ext fields.
	pcrOffsetBase map[uint16]uint64
	pcrOffsetExt  map[uint16]uint64

	// lastCC records the most recent continuity_counter emitted on each
	// PID. Used post-swap to rewrite CCs so the demuxer doesn't see the
	// 4-bit counter jump when the new source starts its own sequence.
	lastCC map[uint16]uint8
	// ccOffset is added mod 16 to incoming CC for each PID post-swap.
	// Populated on the first payload-carrying packet after a swap.
	ccOffset map[uint16]uint8
	// ccSwapPending is like pendingSwap but scoped to CC, since CC
	// applies to many more packets than PTS/PCR and needs its own
	// "first packet since last swap" tracker per PID.
	ccSwapPending map[uint16]bool

	// After MarkSwap, offsets aren't populated yet — a PID's first packet
	// populates its entry. This tracks which PIDs are still awaiting their
	// first post-swap packet (all known PIDs at MarkSwap time).
	awaitingOffset map[uint16]bool

	// carry buffers bytes read from inner that haven't yet formed a
	// complete TS packet — the next Read appends to them until a full
	// 188-byte boundary lands.
	carry []byte
	// emit holds processed packets ready to copy out to the consumer.
	// Lets Read serve arbitrary request sizes without losing alignment.
	emit []byte
}

// New wraps r in a Bridge. The returned Bridge passes bytes through
// unchanged until MarkSwap is called, at which point it rewrites PCR,
// PTS, and DTS on incoming bytes to continue from the last values
// emitted before MarkSwap.
func New(r io.ReadCloser) *Bridge {
	return &Bridge{
		inner:          r,
		lastPTS:        make(map[uint16]uint64),
		lastPCRBase:    make(map[uint16]uint64),
		lastPCRExt:     make(map[uint16]uint64),
		ptsOffset:      make(map[uint16]uint64),
		pcrOffsetBase:  make(map[uint16]uint64),
		pcrOffsetExt:   make(map[uint16]uint64),
		lastCC:         make(map[uint16]uint8),
		ccOffset:       make(map[uint16]uint8),
		ccSwapPending:  make(map[uint16]bool),
		awaitingOffset: make(map[uint16]bool),
	}
}

// MarkSwap signals that the underlying reader's source has changed.
// Subsequent packets get their PCR/PTS/DTS rewritten with a per-PID
// offset that makes the new stream continue from the last values
// emitted on each PID.
//
// Drops any partial-packet carry: at a source boundary, those bytes
// belong to the old stream and mixing them with new-stream bytes
// would produce a malformed packet. A sub-188-byte data loss is
// well inside the player's tolerance.
func (b *Bridge) MarkSwap() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pendingSwap = true
	b.carry = b.carry[:0]
	b.awaitingOffset = make(map[uint16]bool, len(b.lastPTS)+len(b.lastPCRBase))
	for pid := range b.lastPTS {
		b.awaitingOffset[pid] = true
	}
	for pid := range b.lastPCRBase {
		b.awaitingOffset[pid] = true
	}
	// Clear any existing offsets — they belonged to the prior swap.
	for k := range b.ptsOffset {
		delete(b.ptsOffset, k)
	}
	for k := range b.pcrOffsetBase {
		delete(b.pcrOffsetBase, k)
	}
	for k := range b.pcrOffsetExt {
		delete(b.pcrOffsetExt, k)
	}
	for k := range b.ccOffset {
		delete(b.ccOffset, k)
	}
	// Arm CC swap-pending for every PID we've ever seen so the first
	// payload packet on each PID post-swap establishes its own CC offset.
	for pid := range b.lastCC {
		b.ccSwapPending[pid] = true
	}
}

// Read returns processed TS bytes. Works for any buffer size: packets
// are processed into an internal emit buffer and copied out in whatever
// chunks the caller asks for, so unaligned Reads don't desync the
// packet parser.
func (b *Bridge) Read(p []byte) (int, error) {
	for {
		b.mu.Lock()
		if len(b.emit) > 0 {
			n := copy(p, b.emit)
			b.emit = b.emit[n:]
			b.mu.Unlock()
			return n, nil
		}
		b.mu.Unlock()

		tmp := make([]byte, 32*1024)
		n, err := b.inner.Read(tmp)
		if n == 0 {
			return 0, err
		}

		b.mu.Lock()
		b.carry = append(b.carry, tmp[:n]...)
		for len(b.carry) >= tsPacketSize {
			if b.carry[0] != tsSyncByte {
				off := findSync(b.carry)
				if off < 0 {
					// No sync anywhere in the buffer yet — keep what we
					// have and wait for more bytes.
					break
				}
				b.carry = b.carry[off:]
				if len(b.carry) < tsPacketSize {
					break
				}
			}
			pkt := b.carry[:tsPacketSize]
			b.processPacketLocked(pkt)
			b.emit = append(b.emit, pkt...)
			b.carry = b.carry[tsPacketSize:]
		}
		b.mu.Unlock()
		// Loop to serve from emit, or pull more from inner if emit is
		// still empty (only possible when carry < tsPacketSize).
	}
}

// Close closes the wrapped reader.
func (b *Bridge) Close() error { return b.inner.Close() }

// findSync returns the index of the next sync byte in buf such that
// there are at least tsPacketSize bytes starting from it, or -1 if
// no such position exists.
func findSync(buf []byte) int {
	for i := 0; i+tsPacketSize <= len(buf); i++ {
		if buf[i] == tsSyncByte {
			return i
		}
	}
	return -1
}

// processPacketLocked inspects and, if applicable, rewrites a single
// 188-byte TS packet in place. Must hold b.mu.
func (b *Bridge) processPacketLocked(pkt []byte) {
	if len(pkt) != tsPacketSize || pkt[0] != tsSyncByte {
		return
	}

	pusi := pkt[1]&0x40 != 0
	pid := uint16(pkt[1]&0x1f)<<8 | uint16(pkt[2])
	afc := (pkt[3] >> 4) & 0x03
	hasPayload := afc == 0x01 || afc == 0x03

	// Payload start index after optional adaptation field.
	payloadStart := 4
	if afc == 0x02 || afc == 0x03 {
		afLen := int(pkt[4])
		if afLen > 0 && 5+afLen <= tsPacketSize {
			// Parse AF flags if present.
			if afLen >= 1 {
				afFlags := pkt[5]
				hasPCR := afFlags&0x10 != 0
				if hasPCR && 6+6 <= 5+afLen {
					b.handlePCRLocked(pid, pkt[6:12])
				}
			}
		}
		payloadStart = 5 + afLen
	}
	if hasPayload {
		if payloadStart < tsPacketSize && pusi {
			b.handlePESStartLocked(pid, pkt[payloadStart:])
		}
		// CC only increments on payload-carrying packets. Rewrite it
		// after PES/PCR handling so we account for the packet we're
		// about to emit.
		b.handleCCLocked(pid, pkt)
	}
}

// handleCCLocked rewrites the 4-bit continuity_counter in byte 3 so the
// per-PID sequence stays monotonic across bypass swaps. Must hold b.mu.
func (b *Bridge) handleCCLocked(pid uint16, pkt []byte) {
	cc := pkt[3] & 0x0f

	if b.ccSwapPending[pid] {
		// First post-swap payload packet on this PID: compute offset
		// that makes this packet's CC equal (lastCC + 1) mod 16.
		target := (b.lastCC[pid] + 1) & 0x0f
		b.ccOffset[pid] = (target - cc) & 0x0f
		delete(b.ccSwapPending, pid)
	}

	if off, ok := b.ccOffset[pid]; ok && off != 0 {
		newCC := (cc + off) & 0x0f
		pkt[3] = (pkt[3] & 0xf0) | newCC
		cc = newCC
	}
	b.lastCC[pid] = cc
}

// handlePCRLocked reads/rewrites the 6-byte PCR field in place.
func (b *Bridge) handlePCRLocked(pid uint16, pcrBytes []byte) {
	if len(pcrBytes) < 6 {
		return
	}
	base, ext := readPCR(pcrBytes)

	if b.pendingSwap {
		// First PCR seen after swap on this PID → establish offsets.
		if _, ok := b.pcrOffsetBase[pid]; !ok {
			target := (b.lastPCRBase[pid] + ptsContinuationDelta) % pcrBaseModulo
			b.pcrOffsetBase[pid] = (target + pcrBaseModulo - base) % pcrBaseModulo
			// Keep extension aligned; carry is negligible over short swap.
			b.pcrOffsetExt[pid] = (b.lastPCRExt[pid] + pcrExtModulo - ext) % pcrExtModulo
		}
		delete(b.awaitingOffset, pid)
		if len(b.awaitingOffset) == 0 {
			b.pendingSwap = false
		}
	}

	if off, ok := b.pcrOffsetBase[pid]; ok && off != 0 {
		base = (base + off) % pcrBaseModulo
	}
	if off, ok := b.pcrOffsetExt[pid]; ok && off != 0 {
		ext = (ext + off) % pcrExtModulo
	}
	writePCR(pcrBytes, base, ext)

	b.lastPCRBase[pid] = base
	b.lastPCRExt[pid] = ext
}

// handlePESStartLocked reads/rewrites PTS/DTS in a PES header embedded
// in the packet's payload.
func (b *Bridge) handlePESStartLocked(pid uint16, payload []byte) {
	if len(payload) < 14 {
		return
	}
	// packet_start_code_prefix 0x000001
	if payload[0] != 0 || payload[1] != 0 || payload[2] != 1 {
		return
	}
	streamID := payload[3]
	// Stream IDs that actually carry PES timestamps; others (e.g.
	// program_stream_map, padding) don't have PTS/DTS in the header.
	if !streamIDCarriesPTS(streamID) {
		return
	}

	flags := payload[7]
	ptsDTSFlags := (flags >> 6) & 0x03
	if ptsDTSFlags != 0x02 && ptsDTSFlags != 0x03 {
		return
	}
	// PTS at bytes 9..13.
	if len(payload) < 14 {
		return
	}
	pts := readTimestamp(payload[9:14])

	if b.pendingSwap {
		if _, ok := b.ptsOffset[pid]; !ok {
			target := (b.lastPTS[pid] + ptsContinuationDelta) % ptsModulo
			b.ptsOffset[pid] = (target + ptsModulo - pts) % ptsModulo
		}
		delete(b.awaitingOffset, pid)
		if len(b.awaitingOffset) == 0 {
			b.pendingSwap = false
		}
	}

	if off, ok := b.ptsOffset[pid]; ok && off != 0 {
		pts = (pts + off) % ptsModulo
		writeTimestamp(payload[9:14], pts, 0x02)
	}
	b.lastPTS[pid] = pts

	// DTS at bytes 14..19 if ptsDTSFlags == '11'.
	if ptsDTSFlags == 0x03 && len(payload) >= 19 {
		dts := readTimestamp(payload[14:19])
		if off, ok := b.ptsOffset[pid]; ok && off != 0 {
			dts = (dts + off) % ptsModulo
			writeTimestamp(payload[14:19], dts, 0x01)
		}
	}
}

// streamIDCarriesPTS reports whether a PES stream_id indicates a stream
// that carries PTS/DTS in the standard PES header layout.
func streamIDCarriesPTS(id byte) bool {
	switch {
	case id >= 0xc0 && id <= 0xdf: // audio stream_id
		return true
	case id >= 0xe0 && id <= 0xef: // video stream_id
		return true
	}
	return false
}

// readPCR decodes 6 PCR bytes into (base, extension).
func readPCR(b []byte) (uint64, uint64) {
	base := uint64(b[0])<<25 |
		uint64(b[1])<<17 |
		uint64(b[2])<<9 |
		uint64(b[3])<<1 |
		uint64(b[4]>>7)
	ext := (uint64(b[4]&0x01) << 8) | uint64(b[5])
	return base, ext
}

// writePCR encodes (base, extension) into 6 PCR bytes in place.
// Reserved middle 6 bits are set to all 1s as the spec requires.
func writePCR(b []byte, base, ext uint64) {
	b[0] = byte(base >> 25)
	b[1] = byte(base >> 17)
	b[2] = byte(base >> 9)
	b[3] = byte(base >> 1)
	b[4] = byte((base&0x01)<<7) | 0x7e | byte((ext>>8)&0x01)
	b[5] = byte(ext)
}

// readTimestamp decodes a 5-byte PTS/DTS field.
// Layout: '001X' + ts[32:30] + '1' + ts[29:15] + '1' + ts[14:0] + '1'
// where X is 1 for PTS-only, 0 for PTS-before-DTS (top nibble 0010),
// 1 for DTS (top nibble 0001). The marker bits are all 1s.
func readTimestamp(b []byte) uint64 {
	if len(b) < 5 {
		return 0
	}
	var ts uint64
	ts |= uint64(b[0]&0x0e) << 29 // bits 32..30
	ts |= uint64(b[1]) << 22      // bits 29..22
	ts |= uint64(b[2]&0xfe) << 14 // bits 21..15
	ts |= uint64(b[3]) << 7       // bits 14..7
	ts |= uint64(b[4]&0xfe) >> 1  // bits 6..0
	return ts
}

// writeTimestamp encodes a 33-bit timestamp into 5 bytes in place.
// topNibble is the 4-bit prefix: 0x02 for PTS, 0x01 for DTS.
func writeTimestamp(b []byte, ts uint64, topNibble byte) {
	if len(b) < 5 {
		return
	}
	b[0] = (topNibble << 4) | byte((ts>>29)&0x0e) | 0x01
	b[1] = byte(ts >> 22)
	b[2] = byte((ts>>14)&0xfe) | 0x01
	b[3] = byte(ts >> 7)
	b[4] = byte((ts<<1)&0xfe) | 0x01
}
