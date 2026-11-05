// Package upstream provides the safe, bounded HTTP transport used for model providers.
package upstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultRequestTimeout = 120 * time.Second

var ErrPrivateUpstream = errors.New("private or local upstream URLs are disabled")
var ErrResponseTooLarge = errors.New("upstream response exceeds size limit")
var ErrCircuitOpen = errors.New("upstream circuit is temporarily open")

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // shared carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),  // documentation
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

// NewRequest creates an upstream request with a bounded lifetime. The caller's
// context is retained so disconnects cancel work at the provider.
func NewRequest(ctx context.Context, method, rawURL string, body interface{ Read([]byte) (int, error) }) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return http.NewRequestWithContext(ctx, method, rawURL, body)
}

// Client never follows redirects. A redirect would bypass the initial URL
// validation and could otherwise reach an internal service.
var Client = &http.Client{
	Timeout: defaultRequestTimeout,
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         safeDialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		// Bulkhead per provider host: a slow provider can hold at most this many
		// sockets, so it cannot exhaust the pool for everyone else.
		MaxConnsPerHost:       envInt("UPSTREAM_MAX_CONNS_PER_HOST", 256, 8, 4096),
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// streamClient serves streaming responses. It shares Client's transport and
// header timeout but has no whole-request Timeout, which would also cover
// reading the body and cut long generations off mid-stream. DoStream bounds
// streams with an idle deadline and a maximum lifetime instead.
var streamClient = &http.Client{
	Transport:     Client.Transport,
	CheckRedirect: Client.CheckRedirect,
}

var (
	streamIdleTimeout = 90 * time.Second
	streamMaxDuration = 30 * time.Minute
)

type circuitState struct {
	failures  int
	openUntil time.Time
}

var circuitMu sync.Mutex
var circuits = make(map[string]circuitState)

// ConfigureFromEnv applies runtime timeout settings after .env has loaded.
// It must be called during startup, before requests are served.
func ConfigureFromEnv() {
	Client.Timeout = time.Duration(envInt("UPSTREAM_REQUEST_TIMEOUT_SECONDS", 120, 5, 900)) * time.Second
	if transport, ok := Client.Transport.(*http.Transport); ok {
		transport.ResponseHeaderTimeout = time.Duration(envInt("UPSTREAM_RESPONSE_HEADER_TIMEOUT_SECONDS", 30, 2, 300)) * time.Second
	}
	streamIdleTimeout = time.Duration(envInt("UPSTREAM_STREAM_IDLE_TIMEOUT_SECONDS", 90, 5, 900)) * time.Second
	streamMaxDuration = time.Duration(envInt("UPSTREAM_STREAM_MAX_SECONDS", 1800, 60, 7200)) * time.Second
}

// DoStream is Do for streaming responses: no overall timeout, but the body
// is cancelled if the provider sends nothing for the idle timeout or the
// stream outlives the maximum duration. Closing the body releases both.
func DoStream(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, notSentError{errors.New("nil upstream request")}
	}
	ctx, cancel := context.WithTimeout(req.Context(), streamMaxDuration)
	resp, err := doWith(streamClient, req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = newIdleBody(resp.Body, streamIdleTimeout, cancel)
	return resp, nil
}

// idleBody cancels the request when no bytes arrive for the idle period.
type idleBody struct {
	io.ReadCloser
	idle   time.Duration
	timer  *time.Timer
	cancel context.CancelFunc
}

func newIdleBody(rc io.ReadCloser, idle time.Duration, cancel context.CancelFunc) *idleBody {
	return &idleBody{ReadCloser: rc, idle: idle, timer: time.AfterFunc(idle, cancel), cancel: cancel}
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.timer.Reset(b.idle)
	}
	return n, err
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// Do adds bounded retry and a small per-host circuit breaker to the safe HTTP
// client. Requests are retried only when their body can be recreated.
func Do(req *http.Request) (*http.Response, error) {
	return doWith(Client, req)
}

type noStatusRetryKey struct{}

// WithoutStatusRetry marks req so Do and DoStream hand back a 429/502/503/504
// at once instead of retrying the same endpoint. Callers that hold another
// provider key use it to move on rather than wait out one key's rate limit.
func WithoutStatusRetry(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), noStatusRetryKey{}, true))
}

// countsAgainstHost reports whether a failed status says the host is unwell.
// A 429 is one key or account hitting its quota; other keys on the same host
// are fine, so it must not open the shared circuit.
func countsAgainstHost(status int) bool { return status != http.StatusTooManyRequests }

// circuitKey scopes the breaker to one credential on one host, so a customer
// whose key keeps failing does not block that provider for everyone else on
// the replica. Only a hash of the credential is kept.
func circuitKey(req *http.Request) string {
	host := req.URL.Hostname()
	for _, h := range []string{"Authorization", "X-Api-Key", "Api-Key", "X-Goog-Api-Key"} {
		if v := req.Header.Get(h); v != "" {
			sum := sha256.Sum256([]byte(v))
			return host + "#" + hex.EncodeToString(sum[:8])
		}
	}
	return host
}

// neverSent reports whether a transport error happened before any of the
// request reached the provider (DNS, connect), so resending cannot bill twice.
// Anything later, a reset or EOF mid-request, may have been processed.
// notSentError marks a failure that happened before any byte left the proxy.
type notSentError struct{ err error }

func (e notSentError) Error() string { return e.err.Error() }
func (e notSentError) Unwrap() error { return e.err }

// MayHaveReachedProvider reports whether a failed request could have been
// received, and therefore billed, by the provider. Only failures that
// provably happened before sending (validation, open circuit, DNS, dial)
// are excluded; timeouts and mid-flight resets count as possibly billed.
func MayHaveReachedProvider(err error) bool {
	if err == nil {
		return false
	}
	var ns notSentError
	if errors.As(err, &ns) || errors.Is(err, ErrPrivateUpstream) {
		return false
	}
	return !neverSent(err)
}

func neverSent(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// backoff is full-jitter exponential: uniform in [0, 250ms·2^(attempt-1)], capped at 2s.
func backoff(attempt int) time.Duration {
	ceiling := 250 * time.Millisecond << (attempt - 1)
	if ceiling <= 0 || ceiling > 2*time.Second {
		ceiling = 2 * time.Second
	}
	return time.Duration(rand.Int64N(int64(ceiling) + 1)) // #nosec G404 -- jitter, not security
}

func doWith(client *http.Client, req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, notSentError{errors.New("nil upstream request")}
	}
	if err := ValidateURL(req.URL.String()); err != nil {
		return nil, notSentError{err}
	}
	host := circuitKey(req)
	if circuitIsOpen(host, time.Now()) {
		return nil, notSentError{ErrCircuitOpen}
	}
	attempts := envInt("UPSTREAM_MAX_ATTEMPTS", 2, 1, 4)
	if req.Body != nil && req.GetBody == nil {
		attempts = 1
	}
	statusRetry := req.Context().Value(noStatusRetryKey{}) == nil
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = body
		}
		// #nosec G704 -- ValidateURL above, safeDialContext rejects non-public
		// resolved addresses, and redirects are disabled on Client.
		resp, err := client.Do(req)
		if err == nil && !retryableStatus(resp.StatusCode) {
			if failedStatus(resp.StatusCode) {
				circuitFailed(host, time.Now())
			} else {
				circuitSucceeded(host)
			}
			return resp, nil
		}
		if err == nil {
			lastErr = fmt.Errorf("upstream returned %s", resp.Status)
			if attempt == attempts || !statusRetry {
				if countsAgainstHost(resp.StatusCode) {
					circuitFailed(host, time.Now())
				}
				return resp, nil
			}
			delay := retryDelay(resp, attempt)
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			if err := waitForRetry(req.Context(), delay); err != nil {
				return nil, err
			}
			continue
		}
		lastErr = err
		// Only resend what provably never reached the provider: a POST that
		// died mid-flight may already be generating (and billing) a reply.
		// A timeout already cost the caller its full budget, so it is final too.
		if isTimeout(err) || !neverSent(err) {
			break
		}
		if attempt < attempts {
			if err := waitForRetry(req.Context(), backoff(attempt)); err != nil {
				return nil, err
			}
		}
	}
	circuitFailed(host, time.Now())
	return nil, lastErr
}

// retryableStatus is a status worth resending the same request for. 429 and
// 503 mean the provider refused it; a 502 or 504 can come from a gateway
// after the provider started work, so those are handed back instead (the
// proxy can still move to another key or fallback hop).
func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
}

// failedStatus is a status that counts as an upstream failure for the breaker.
func failedStatus(status int) bool {
	return retryableStatus(status) || status == http.StatusBadGateway || status == http.StatusGatewayTimeout
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	if raw := strings.TrimSpace(resp.Header.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 && seconds <= 10 {
			return time.Duration(seconds) * time.Second
		}
		if when, err := http.ParseTime(raw); err == nil {
			delay := time.Until(when)
			if delay >= 0 && delay <= 10*time.Second {
				return delay
			}
		}
	}
	return backoff(attempt)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func circuitIsOpen(host string, now time.Time) bool {
	circuitMu.Lock()
	defer circuitMu.Unlock()
	state := circuits[host]
	if !state.openUntil.IsZero() && now.After(state.openUntil) {
		delete(circuits, host)
		return false
	}
	return now.Before(state.openUntil)
}

func circuitSucceeded(host string) {
	circuitMu.Lock()
	delete(circuits, host)
	circuitMu.Unlock()
}

func circuitFailed(host string, now time.Time) {
	circuitMu.Lock()
	defer circuitMu.Unlock()
	state := circuits[host]
	state.failures++
	threshold := envInt("UPSTREAM_CIRCUIT_FAILURES", 5, 2, 100)
	if state.failures >= threshold {
		state.openUntil = now.Add(time.Duration(envInt("UPSTREAM_CIRCUIT_COOLDOWN_SECONDS", 30, 1, 600)) * time.Second)
		if state.failures == threshold {
			slog.Warn("upstream_circuit_opened", "host", host, "failures", state.failures, "open_until", state.openUntil)
		}
	}
	circuits[host] = state
}

func envInt(name string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}

// ValidateURL rejects malformed URLs and, unless expressly enabled for a
// self-hosted deployment, destinations on private networks.
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return fmt.Errorf("invalid upstream URL")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("upstream URL must use http or https")
	}
	if u.User != nil {
		return fmt.Errorf("upstream URL must not include credentials")
	}
	if allowPrivateUpstreams() {
		return nil
	}
	return validateHost(u.Hostname())
}

func safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if allowPrivateUpstreams() {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve upstream host: %w", err)
	}
	for _, ip := range addrs {
		if !isPrivateAddress(ip) {
			var d net.Dialer
			// Dial the verified address, rather than the hostname, to prevent DNS
			// rebinding between validation and connection.
			return d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
	}
	return nil, ErrPrivateUpstream
}

func allowPrivateUpstreams() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SPENDMUX_ALLOW_PRIVATE_UPSTREAMS")))
	return v == "1" || v == "true" || v == "yes"
}

func validateHost(host string) error {
	if ip, err := netip.ParseAddr(host); err == nil {
		if isPrivateAddress(ip) {
			return ErrPrivateUpstream
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", host)
	if err != nil {
		return fmt.Errorf("resolve upstream host: %w", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("upstream host resolved to no addresses")
	}
	for _, ip := range addrs {
		if isPrivateAddress(ip) {
			return ErrPrivateUpstream
		}
	}
	return nil
}

func isPrivateAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// ReadAllLimit reads at most limit bytes and reports overflow rather than
// returning a truncated response as though it were complete.
func ReadAllLimit(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, ErrResponseTooLarge
	}
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, ErrResponseTooLarge
	}
	return b, nil
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// DescribeError turns a transport-level failure into a sentence a person can
// act on, naming the provider and the timeout that fired.
func DescribeError(provider string, err error) string {
	if provider == "" {
		provider = "upstream"
	}
	switch {
	case err == nil:
		return provider + ": request failed"
	case errors.Is(err, ErrCircuitOpen):
		return provider + ": paused for a moment after repeated failures (circuit breaker); try again shortly"
	case isTimeout(err):
		hdr := envInt("UPSTREAM_RESPONSE_HEADER_TIMEOUT_SECONDS", 30, 2, 300)
		return fmt.Sprintf("%s: no response within %ds (timed out)", provider, hdr)
	case errors.Is(err, context.Canceled):
		return provider + ": request cancelled"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such host"):
		return provider + ": DNS lookup failed"
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "connection reset"):
		return provider + ": connection refused or reset"
	case strings.Contains(msg, "tls"), strings.Contains(msg, "certificate"):
		return provider + ": TLS handshake failed"
	}
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return provider + ": " + msg
}
