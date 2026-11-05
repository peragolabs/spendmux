package upstream

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestValidateURLRejectsUnsafeDestinations(t *testing.T) {
	tests := []string{
		"http://127.0.0.1:8080",
		"http://169.254.169.254/latest/meta-data",
		"http://10.0.0.1",
		"http://100.64.0.1",
		"http://[::1]",
		"file:///etc/passwd",
		"https://user:pass@example.com",
	}
	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			if err := ValidateURL(rawURL); err == nil {
				t.Fatalf("ValidateURL(%q) succeeded", rawURL)
			}
		})
	}
}

func TestValidateURLAcceptsPublicIPAddress(t *testing.T) {
	if err := ValidateURL("https://8.8.8.8/v1"); err != nil {
		t.Fatalf("ValidateURL rejected public address: %v", err)
	}
}

func TestReadAllLimitRejectsOverflow(t *testing.T) {
	if _, err := ReadAllLimit(strings.NewReader("1234"), 3); err != ErrResponseTooLarge {
		t.Fatalf("ReadAllLimit overflow error = %v, want %v", err, ErrResponseTooLarge)
	}
	b, err := ReadAllLimit(strings.NewReader("123"), 3)
	if err != nil || string(b) != "123" {
		t.Fatalf("ReadAllLimit exact limit = %q, %v", b, err)
	}
}

func TestDoRetriesRetryableResponse(t *testing.T) {
	oldClient := Client
	defer func() { Client = oldClient }()
	t.Setenv("UPSTREAM_MAX_ATTEMPTS", "2")
	var calls atomic.Int32
	Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		status := http.StatusOK
		if calls.Add(1) == 1 {
			status = http.StatusServiceUnavailable
		}
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("response")),
		}, nil
	})}
	req, err := http.NewRequest(http.MethodPost, "https://8.8.8.8/v1/chat", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d", resp.StatusCode, calls.Load())
	}
}

func statusClient(status int, calls *atomic.Int32) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     http.Header{"Retry-After": []string{"0"}},
			Body:       io.NopCloser(strings.NewReader("{}")),
		}, nil
	})}
}

func TestWithoutStatusRetryReturnsFirstResponse(t *testing.T) {
	oldClient := Client
	defer func() { Client = oldClient }()
	t.Setenv("UPSTREAM_MAX_ATTEMPTS", "3")
	var calls atomic.Int32
	Client = statusClient(http.StatusTooManyRequests, &calls)
	req, _ := http.NewRequest(http.MethodPost, "https://8.8.4.4/v1/chat", bytes.NewReader([]byte("{}")))
	resp, err := Do(WithoutStatusRetry(req))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || calls.Load() != 1 {
		t.Fatalf("status=%d calls=%d, want 429 after one call", resp.StatusCode, calls.Load())
	}
}

func TestRateLimitsDoNotOpenHostCircuit(t *testing.T) {
	oldClient := Client
	defer func() { Client = oldClient }()
	t.Setenv("UPSTREAM_MAX_ATTEMPTS", "1")
	t.Setenv("UPSTREAM_CIRCUIT_FAILURES", "2")
	var calls atomic.Int32
	Client = statusClient(http.StatusTooManyRequests, &calls)
	const host = "https://1.0.0.1/v1/chat"
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest(http.MethodPost, host, bytes.NewReader([]byte("{}")))
		resp, err := Do(req)
		if err != nil {
			t.Fatalf("call %d: %v (a 429 on one key must not block the host)", i, err)
		}
		resp.Body.Close()
	}
	if calls.Load() != 5 {
		t.Fatalf("calls=%d, want 5", calls.Load())
	}
}

func errClient(err error, calls *atomic.Int32) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, err
	})}
}

func TestOnlyUnsentRequestsAreRetried(t *testing.T) {
	oldClient := Client
	defer func() { Client = oldClient }()
	t.Setenv("UPSTREAM_MAX_ATTEMPTS", "3")
	cases := []struct {
		name  string
		err   error
		calls int32
	}{
		{"connection refused", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, 3},
		{"dns", &net.DNSError{Err: "no such host", Name: "x"}, 3},
		{"reset mid-request", &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}, 1},
		{"eof", io.ErrUnexpectedEOF, 1},
	}
	for _, tc := range cases {
		var calls atomic.Int32
		Client = errClient(tc.err, &calls)
		req, _ := http.NewRequest(http.MethodPost, "https://9.9.9.9/v1/chat", bytes.NewReader([]byte("{}")))
		if _, err := Do(req); err == nil {
			t.Fatalf("%s: want error", tc.name)
		}
		if calls.Load() != tc.calls {
			t.Fatalf("%s: %d calls, want %d", tc.name, calls.Load(), tc.calls)
		}
	}
}

func TestGatewayErrorsAreNotResent(t *testing.T) {
	oldClient := Client
	defer func() { Client = oldClient }()
	t.Setenv("UPSTREAM_MAX_ATTEMPTS", "3")
	for _, status := range []int{http.StatusBadGateway, http.StatusGatewayTimeout} {
		var calls atomic.Int32
		Client = statusClient(status, &calls)
		req, _ := http.NewRequest(http.MethodPost, "https://9.9.9.10/v1/chat", bytes.NewReader([]byte("{}")))
		resp, err := Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if calls.Load() != 1 {
			t.Fatalf("%d: %d calls; a gateway error may follow work the provider billed", status, calls.Load())
		}
	}
}

func TestCircuitIsPerCredential(t *testing.T) {
	oldClient := Client
	defer func() { Client = oldClient }()
	t.Setenv("UPSTREAM_MAX_ATTEMPTS", "1")
	t.Setenv("UPSTREAM_CIRCUIT_FAILURES", "2")
	var calls atomic.Int32
	Client = statusClient(http.StatusServiceUnavailable, &calls)
	const url = "https://9.9.9.11/v1/chat"
	call := func(key string) error {
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte("{}")))
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := Do(req)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	for i := 0; i < 3; i++ {
		_ = call("bad-key")
	}
	if err := call("bad-key"); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("failing key should trip its own circuit, got %v", err)
	}
	if err := call("good-key"); err != nil {
		t.Fatalf("another customer's key on the same host must not be blocked: %v", err)
	}
}

func TestBackoffIsJitteredAndCapped(t *testing.T) {
	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		d := backoff(1)
		if d < 0 || d > 250*time.Millisecond {
			t.Fatalf("attempt 1 backoff %v out of range", d)
		}
		seen[d] = true
		if b := backoff(10); b > 2*time.Second {
			t.Fatalf("backoff not capped: %v", b)
		}
	}
	if len(seen) < 10 {
		t.Fatal("backoff is not jittered")
	}
}

func TestMayHaveReachedProvider(t *testing.T) {
	bad, _ := http.NewRequest(http.MethodPost, "ftp://example.com/x", nil)
	if _, err := Do(bad); err == nil || MayHaveReachedProvider(err) {
		t.Fatalf("a request rejected before sending counted as possibly billed: %v", err)
	}
	if !MayHaveReachedProvider(errors.New("read: connection reset by peer")) {
		t.Fatal("a mid-flight failure must count as possibly billed")
	}
	if MayHaveReachedProvider(nil) {
		t.Fatal("nil error")
	}
}
