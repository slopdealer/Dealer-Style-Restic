package repository

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
)

type countingCounter struct{ n uint64 }

func (c *countingCounter) Add(v uint64)          { c.n += v }
func (c *countingCounter) SetMax(uint64)         {}
func (c *countingCounter) Get() (uint64, uint64) { return c.n, 0 }
func (c *countingCounter) Done()                 {}

// Random pack sequences with read errors and save errors at random places.
// For every sequence the worker loop must
//   - read at most one pack while another one is still unsettled, and none
//     when the two together exceed the descriptor bound;
//   - have joined the saves of every pack it read when it returns, on every
//     path (a pack is joined by finish or by drain, exactly once);
//   - return the error of the first failing pack in pack order, and nil only
//     if every pack was read and every save succeeded;
//   - count exactly the packs that were settled without an error.
func TestRepackCompletionOverlapRandomSequences(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261007))
	for seq := 0; seq < 20000; seq++ {
		n := rnd.Intn(9)
		type packPlan struct {
			blobs   int
			readErr error
			saveErr error
		}
		plans := make([]packPlan, n)
		for i := range plans {
			switch rnd.Intn(6) {
			case 0:
				plans[i].blobs = 0
			case 1:
				plans[i].blobs = maxRepackOverlapDescriptors + rnd.Intn(3) - 1
			case 2:
				plans[i].blobs = 2*maxRepackOverlapDescriptors + rnd.Intn(100)
			default:
				plans[i].blobs = 1 + rnd.Intn(maxRepackOverlapDescriptors/2+20)
			}
			if rnd.Intn(12) == 0 {
				plans[i].readErr = fmt.Errorf("read error in pack %d", i)
			}
			if rnd.Intn(12) == 0 {
				plans[i].saveErr = fmt.Errorf("save error in pack %d", i)
			}
		}

		queue := make(chan index.PackBlobs, n)
		for i := range plans {
			queue <- index.PackBlobs{Blobs: make(pack.Blobs, plans[i].blobs)}
		}
		close(queue)

		started := 0
		joined := make([]int, n)   // times the saves of pack i were joined
		open := map[int]struct{}{} // read, not joined yet
		openBlobs := 0
		var wantErr error
		fail := func(format string, args ...any) {
			t.Helper()
			t.Fatalf("sequence %d (%+v): %s", seq, plans, fmt.Sprintf(format, args...))
		}
		progress := &countingCounter{}
		err := runRepackCompletionOverlap(queue, func(task index.PackBlobs) *repackPackCompletion {
			i := started
			started++
			if len(task.Blobs) != plans[i].blobs {
				fail("pack %d arrived out of order", i)
			}
			if len(open) > 1 {
				fail("pack %d is read while %d packs are unsettled", i, len(open))
			}
			if len(open) == 1 && openBlobs+plans[i].blobs > maxRepackOverlapDescriptors {
				fail("pack %d (%d blobs) is read beside an unsettled pack of %d blobs", i, plans[i].blobs, openBlobs)
			}
			open[i] = struct{}{}
			openBlobs += plans[i].blobs
			join := func() {
				joined[i]++
				delete(open, i)
				openBlobs -= plans[i].blobs
			}
			return &repackPackCompletion{
				readErr:     plans[i].readErr,
				descriptors: plans[i].blobs,
				finish: func() error {
					join()
					if plans[i].readErr != nil {
						return plans[i].readErr
					}
					return plans[i].saveErr
				},
				drain: join,
			}
		}, progress)

		settledOK := uint64(0)
		for i := range plans {
			if plans[i].readErr != nil {
				wantErr = plans[i].readErr
				break
			}
			if plans[i].saveErr != nil {
				wantErr = plans[i].saveErr
				break
			}
			settledOK++
		}
		if !errors.Is(err, wantErr) || (wantErr == nil) != (err == nil) {
			fail("returned %v, want %v", err, wantErr)
		}
		if len(open) != 0 {
			fail("returned with the saves of %d packs not joined", len(open))
		}
		for i := 0; i < started; i++ {
			if joined[i] != 1 {
				fail("the saves of pack %d were joined %d times", i, joined[i])
			}
		}
		if wantErr == nil && started != n {
			fail("returned nil after %d of %d packs", started, n)
		}
		if got, _ := progress.Get(); got != settledOK {
			fail("progress counts %d packs, %d were settled without an error", got, settledOK)
		}
	}
}
