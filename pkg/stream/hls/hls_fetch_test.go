package hls

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFetchWithRetry_SuccessFirstAttempt(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	h := &HLSStream{Client: srv.Client()}
	var got string
	err := h.fetchWithRetry(context.Background(), 3,
		func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		},
		func(resp *http.Response) error {
			buf := make([]byte, 1024)
			n, _ := resp.Body.Read(buf)
			got = string(buf[:n])
			return nil
		},
	)
	if err != nil {
		t.Fatalf("fetchWithRetry: %v", err)
	}
	if got != "ok" {
		t.Errorf("body = %q, want ok", got)
	}
	if c := atomic.LoadInt32(&calls); c != 1 {
		t.Errorf("calls = %d, want 1", c)
	}
}

func TestFetchWithRetry_RetriesOn429ThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			// Use 0 seconds so retry is immediate.
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, "recovered")
	}))
	defer srv.Close()

	h := &HLSStream{Client: srv.Client()}
	var got string
	err := h.fetchWithRetry(context.Background(), 3,
		func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		},
		func(resp *http.Response) error {
			buf := make([]byte, 64)
			n, _ := resp.Body.Read(buf)
			got = string(buf[:n])
			return nil
		},
	)
	if err != nil {
		t.Fatalf("fetchWithRetry: %v", err)
	}
	if got != "recovered" {
		t.Errorf("body = %q, want recovered", got)
	}
	if c := atomic.LoadInt32(&calls); c != 2 {
		t.Errorf("calls = %d, want 2", c)
	}
}

func TestFetchWithRetry_ExhaustsAttemptsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := &HLSStream{Client: srv.Client()}
	err := h.fetchWithRetry(context.Background(), 2,
		func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		},
		func(resp *http.Response) error {
			return nil
		},
	)
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("expected HTTP 500 in error, got %q", err)
	}
}

func TestFetchWithRetry_ContextCancelShortCircuits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before invoking

	h := &HLSStream{Client: srv.Client()}
	err := h.fetchWithRetry(ctx, 5,
		func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		},
		func(resp *http.Response) error { return nil },
	)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestFetchWithRetry_HandlerErrorRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	h := &HLSStream{Client: srv.Client()}
	attempt := 0
	err := h.fetchWithRetry(context.Background(), 3,
		func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		},
		func(resp *http.Response) error {
			attempt++
			if attempt == 1 {
				return fmt.Errorf("handler fail")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("fetchWithRetry: %v", err)
	}
	if c := atomic.LoadInt32(&calls); c != 2 {
		t.Errorf("server calls = %d, want 2 (handler failure retries)", c)
	}
}
