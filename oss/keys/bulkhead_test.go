package keys

import (
	"testing"

	"github.com/google/uuid"
)

func TestBulkheadCapsPerToken(t *testing.T) {
	inflightMax = 2
	a, b := uuid.New(), uuid.New()
	r1, err := AcquireInFlight(a)
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := AcquireInFlight(a)
	if _, err := AcquireInFlight(a); err == nil {
		t.Fatal("third request on token a should be refused")
	}
	if _, err := AcquireInFlight(b); err != nil {
		t.Fatal("token b must not be affected by token a")
	}
	r1()
	r1() // double release is harmless
	if InFlight(a) != 1 {
		t.Fatalf("expected 1 in flight, got %d", InFlight(a))
	}
	r2()
	if InFlight(a) != 0 {
		t.Fatal("expected slot fully released")
	}
}
