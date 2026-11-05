package keys

import (
	"errors"
	"os"
	"strconv"
	"sync"

	"github.com/google/uuid"
)

// Bulkhead: a per-token cap on in-flight requests, so one runaway client
// (a loop, a stuck agent, a script without a limiter) cannot occupy every
// upstream connection and starve other tokens. RPM caps limit *rate*; this
// limits *concurrency*. Per replica; SPENDMUX_MAX_INFLIGHT_PER_TOKEN, 0 = off.

var ErrTooManyInFlight = errors.New("too many concurrent requests on this token")

var (
	inflightMu  sync.Mutex
	inflight    = map[uuid.UUID]int{}
	inflightMax = func() int {
		if v, err := strconv.Atoi(os.Getenv("SPENDMUX_MAX_INFLIGHT_PER_TOKEN")); err == nil && v >= 0 {
			return v
		}
		return 32
	}()
)

// AcquireInFlight reserves a slot for keyID. The returned func releases it;
// it is safe to call more than once.
func AcquireInFlight(keyID uuid.UUID) (release func(), err error) {
	if inflightMax <= 0 {
		return func() {}, nil
	}
	inflightMu.Lock()
	defer inflightMu.Unlock()
	if inflight[keyID] >= inflightMax {
		return nil, ErrTooManyInFlight
	}
	inflight[keyID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			inflightMu.Lock()
			defer inflightMu.Unlock()
			if inflight[keyID] <= 1 {
				delete(inflight, keyID)
			} else {
				inflight[keyID]--
			}
		})
	}, nil
}

// InFlight reports current in-flight requests for a token (for /metrics, tests).
func InFlight(keyID uuid.UUID) int {
	inflightMu.Lock()
	defer inflightMu.Unlock()
	return inflight[keyID]
}
