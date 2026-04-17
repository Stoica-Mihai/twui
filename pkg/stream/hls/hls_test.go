package hls

import (
	"crypto/aes"
	"testing"
	"time"
)

// --- retryDelay ---

func TestRetryDelay_UsesRetryAfterWhenPresent(t *testing.T) {
	got := retryDelay(0, time.Second, "5")
	if got != 5*time.Second {
		t.Errorf("retryDelay with Retry-After=5 returned %v, want 5s", got)
	}
}

func TestRetryDelay_CapsRetryAfterAt30s(t *testing.T) {
	got := retryDelay(0, time.Second, "60")
	if got != 30*time.Second {
		t.Errorf("retryDelay with Retry-After=60 returned %v, want 30s (capped)", got)
	}
}

func TestRetryDelay_FallsBackToJitterWhenNoHeader(t *testing.T) {
	// With empty header, retryDelay uses jitter. The result should be
	// non-negative and bounded by baseDelay * 2^attempt.
	for i := 0; i < 100; i++ {
		got := retryDelay(2, time.Second, "")
		maxDelay := time.Duration(int64(time.Second) * (1 << 2)) // 4s
		if got < 0 || got > maxDelay {
			t.Errorf("retryDelay(2, 1s, \"\") = %v, out of range [0, %v]", got, maxDelay)
		}
	}
}

func TestRetryDelay_FallsBackToJitterWhenInvalidHeader(t *testing.T) {
	got := retryDelay(0, time.Second, "not-a-number")
	// With attempt 0, maxDelay = baseDelay * 2^0 = 1s
	if got < 0 || got > time.Second {
		t.Errorf("retryDelay(0, 1s, \"not-a-number\") = %v, out of range [0, 1s]", got)
	}
}

// --- keyCache ---

func TestKeyCache_GetMiss(t *testing.T) {
	kc := &keyCache{data: make(map[string][]byte)}
	_, ok := kc.get("missing")
	if ok {
		t.Error("get on empty cache should return false")
	}
}

func TestKeyCache_SetAndGet(t *testing.T) {
	kc := &keyCache{data: make(map[string][]byte)}
	key := []byte("0123456789abcdef")
	kc.set("https://example.com/key1", key)

	got, ok := kc.get("https://example.com/key1")
	if !ok {
		t.Fatal("get returned false after set")
	}
	if string(got) != string(key) {
		t.Errorf("got %x, want %x", got, key)
	}
}

func TestKeyCache_SetDuplicate(t *testing.T) {
	kc := &keyCache{data: make(map[string][]byte)}
	key1 := []byte("aaaaaaaaaaaaaaaa")
	key2 := []byte("bbbbbbbbbbbbbbbb")

	kc.set("uri", key1)
	kc.set("uri", key2) // should be a no-op

	got, _ := kc.get("uri")
	if string(got) != string(key1) {
		t.Errorf("duplicate set should not overwrite; got %x, want %x", got, key1)
	}
}

func TestKeyCache_Eviction(t *testing.T) {
	kc := &keyCache{data: make(map[string][]byte)}

	// Fill the cache to the limit.
	for i := 0; i < maxKeyCacheSize; i++ {
		uri := string(rune('A' + i))
		kc.set(uri, []byte("key"))
	}

	// Add one more to trigger eviction.
	kc.set("overflow", []byte("newkey"))

	// The first entry (rune 'A') should have been evicted.
	if _, ok := kc.get(string(rune('A'))); ok {
		t.Error("oldest entry should have been evicted")
	}
	// The new entry should be present.
	if _, ok := kc.get("overflow"); !ok {
		t.Error("newly added entry should be present")
	}
	// Total size should still be maxKeyCacheSize.
	if len(kc.data) != maxKeyCacheSize {
		t.Errorf("cache size = %d, want %d", len(kc.data), maxKeyCacheSize)
	}
}

// --- decryptAES128CBC ---

func TestDecryptAES128CBC_ValidData(t *testing.T) {
	key := []byte("0123456789abcdef") // 16 bytes
	iv := []byte("abcdef0123456789")  // 16 bytes

	// Encrypt some known plaintext with PKCS#7 padding.
	plaintext := []byte("hello, world!!") // 14 bytes
	padLen := aes.BlockSize - (len(plaintext) % aes.BlockSize)
	padded := make([]byte, len(plaintext)+padLen)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}

	// Encrypt using CBC.
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}

	ciphertext := make([]byte, len(padded))
	prev := iv
	for i := 0; i < len(padded); i += aes.BlockSize {
		blk := make([]byte, aes.BlockSize)
		for j := 0; j < aes.BlockSize; j++ {
			blk[j] = padded[i+j] ^ prev[j]
		}
		block.Encrypt(ciphertext[i:i+aes.BlockSize], blk)
		prev = ciphertext[i : i+aes.BlockSize]
	}

	got, err := decryptAES128CBC(ciphertext, key, iv)
	if err != nil {
		t.Fatalf("decryptAES128CBC error: %v", err)
	}
	if string(got) != "hello, world!!" {
		t.Errorf("decrypted = %q, want %q", string(got), "hello, world!!")
	}
}

func TestDecryptAES128CBC_InvalidIVLength(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv := []byte("short")
	data := make([]byte, 16)

	_, err := decryptAES128CBC(data, key, iv)
	if err == nil {
		t.Error("expected error for invalid IV length")
	}
}

func TestDecryptAES128CBC_EmptyData(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv := []byte("abcdef0123456789")

	_, err := decryptAES128CBC(nil, key, iv)
	if err == nil {
		t.Error("expected error for empty data")
	}
}

func TestDecryptAES128CBC_NonBlockSizeData(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv := []byte("abcdef0123456789")
	data := make([]byte, 15) // not a multiple of 16

	_, err := decryptAES128CBC(data, key, iv)
	if err == nil {
		t.Error("expected error for non-block-size data")
	}
}

func TestDecryptAES128CBC_InvalidKeyLength(t *testing.T) {
	key := []byte("short")
	iv := []byte("abcdef0123456789")
	data := make([]byte, 16)

	_, err := decryptAES128CBC(data, key, iv)
	if err == nil {
		t.Error("expected error for invalid key length")
	}
}

// --- segmentChanSize ---

func TestSegmentChanSize(t *testing.T) {
	if got := segmentChanSize(); got != liveSegmentChanSize {
		t.Errorf("segmentChanSize() = %d, want %d", got, liveSegmentChanSize)
	}
}

// --- HLSStream URL and SetOnDrop ---

func TestHLSStream_URL(t *testing.T) {
	h := &HLSStream{StreamURL: "https://example.com/stream.m3u8"}
	if got := h.URL(); got != "https://example.com/stream.m3u8" {
		t.Errorf("URL() = %q, want %q", got, "https://example.com/stream.m3u8")
	}
}

func TestHLSStream_SetOnDrop(t *testing.T) {
	h := &HLSStream{}
	called := false
	h.SetOnDrop(func(err error) { called = true })

	// Read the callback back through the internal field.
	h.dropMu.Lock()
	fn := h.onStreamDrop
	h.dropMu.Unlock()

	if fn == nil {
		t.Fatal("onStreamDrop callback was not set")
	}
	fn(nil)
	if !called {
		t.Error("callback was not invoked")
	}
}

func TestHLSStream_SetOnDrop_Nil(t *testing.T) {
	h := &HLSStream{}
	// Setting nil should not panic.
	h.SetOnDrop(nil)

	h.dropMu.Lock()
	fn := h.onStreamDrop
	h.dropMu.Unlock()

	if fn != nil {
		t.Error("onStreamDrop should be nil after SetOnDrop(nil)")
	}
}
