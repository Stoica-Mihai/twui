package stream

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
)

type HTTPStream struct {
	StreamURL string
	Client    *http.Client
	Headers   map[string]string
	// ChunkSize, if > 0, fetches the URL in range-request chunks of this many
	// bytes. Required for CDNs that reject unbounded GET requests (e.g. YouTube).
	ChunkSize int64
	// ContentLength is the total file size. When ChunkSize > 0 and
	// ContentLength is 0, the size is discovered from the first response's
	// Content-Range header.
	ContentLength int64
}

func (h *HTTPStream) Open() (io.ReadCloser, error) {
	if h.ChunkSize > 0 {
		return h.openChunked()
	}
	req, err := http.NewRequest(http.MethodGet, h.StreamURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("http stream: unexpected status %d for %s", resp.StatusCode, h.StreamURL)
	}
	return resp.Body, nil
}

// openChunked returns a reader that fetches the URL in successive range
// requests of h.ChunkSize bytes, stitching them into a single stream.
func (h *HTTPStream) openChunked() (io.ReadCloser, error) {
	cr := &chunkReader{
		url:       h.StreamURL,
		client:    h.Client,
		headers:   h.Headers,
		chunkSize: h.ChunkSize,
		total:     h.ContentLength,
		pos:       0,
	}
	// Fetch the first chunk to validate and discover total size.
	if err := cr.fetchNext(); err != nil {
		return nil, err
	}
	return cr, nil
}

// chunkReader implements io.ReadCloser over a sequence of range requests.
type chunkReader struct {
	url       string
	client    *http.Client
	headers   map[string]string
	chunkSize int64
	total     int64 // 0 = unknown until first response
	pos       int64 // next byte to request
	cur       io.ReadCloser
	done      bool
}

func (c *chunkReader) fetchNext() error {
	if c.done {
		return nil
	}
	end := c.pos + c.chunkSize - 1
	rangeHdr := fmt.Sprintf("bytes=%d-%d", c.pos, end)

	req, err := http.NewRequest(http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("http chunk: create request: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Range", rangeHdr)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("http chunk: request: %w", err)
	}
	if resp.StatusCode != http.StatusPartialContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return fmt.Errorf("http chunk: unexpected status %d (range %s) for %s", resp.StatusCode, rangeHdr, c.url)
	}

	// Discover total size from Content-Range: bytes start-end/total
	if c.total == 0 {
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			// format: "bytes start-end/total"
			if i := lastIndex(cr, "/"); i >= 0 {
				if n, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
					c.total = n
				}
			}
		}
	}

	if c.cur != nil {
		c.cur.Close()
	}
	c.cur = resp.Body
	c.pos += resp.ContentLength
	if c.total > 0 && c.pos >= c.total {
		c.done = true
	}
	return nil
}

func (c *chunkReader) Read(p []byte) (int, error) {
	for {
		if c.cur == nil {
			if c.done {
				return 0, io.EOF
			}
			if err := c.fetchNext(); err != nil {
				return 0, err
			}
		}
		n, err := c.cur.Read(p)
		if n > 0 {
			return n, nil
		}
		if err == io.EOF {
			c.cur.Close()
			c.cur = nil
			if c.done {
				return 0, io.EOF
			}
			// Fetch the next chunk.
			if err2 := c.fetchNext(); err2 != nil {
				return 0, err2
			}
			continue
		}
		return n, err
	}
}

func (c *chunkReader) Close() error {
	c.done = true
	if c.cur != nil {
		err := c.cur.Close()
		c.cur = nil
		return err
	}
	return nil
}

// lastIndex returns the last index of sep in s, or -1.
func lastIndex(s, sep string) int {
	idx := -1
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			idx = i
		}
	}
	return idx
}

func (h *HTTPStream) URL() string {
	return h.StreamURL
}
