package retry

import (
	"context"
	"reflect"
	"testing"
)

func TestHeldReports(t *testing.T) {
	var got []string
	say := func(ctx context.Context, s string) {
		report(ctx, func() { got = append(got, s) })
	}

	// without a holder a report is made at once
	say(context.Background(), "direct")
	if !reflect.DeepEqual(got, []string{"direct"}) {
		t.Fatalf("got %v", got)
	}

	// held until released, in order; later reports pass
	got = nil
	ctx, held := WithHeldReports(context.Background())
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	say(ctx, "one")
	say(child, "two")
	if got != nil {
		t.Fatalf("reported while held: %v", got)
	}
	held.Release()
	say(child, "three")
	held.Drop() // nothing after Release
	say(ctx, "four")
	if want := []string{"one", "two", "three", "four"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// dropped for good
	got = nil
	ctx, held = WithHeldReports(context.Background())
	say(ctx, "one")
	held.Drop()
	say(ctx, "two")
	held.Release() // nothing after Drop
	say(ctx, "three")
	if got != nil {
		t.Fatalf("reported after Drop: %v", got)
	}

	// a nil holder is accepted
	var none *HeldReports
	none.Release()
	none.Drop()
}
