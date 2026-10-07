package repository

import (
	"context"
	"errors"
	"testing"
)

// TestAsyncSaveResultSuppressionIsNarrow is the reviewer's witness for red
// team fix 2 (TestWitnessFinal3Fix2SuppressionIsNarrow): what a save reports
// to the uploader's group. An error is withheld only when the context of the
// save is a repack's AND that context is cancelled; on every other context
// (the archiver's, cancelled or not) and on a live repack context the error
// is reported as before.
func TestAsyncSaveResultSuppressionIsNarrow(t *testing.T) {
	failure := errors.New("a real save error")
	live := context.Background()
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	markedLive := withRepackSaves(context.Background())
	markedDead, cancel2 := context.WithCancel(withRepackSaves(context.Background()))
	cancel2()

	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want error
	}{
		{"archiver context, save failed", live, failure, failure},
		{"archiver context, cancelled, save failed", dead, failure, failure},
		{"archiver context, cancelled, save cancelled", dead, context.Canceled, context.Canceled},
		{"repack context, alive, save failed", markedLive, failure, failure},
		{"repack context, alive, no error", markedLive, nil, nil},
		{"repack context, cancelled, save cancelled", markedDead, context.Canceled, nil},
		{"repack context, cancelled, save failed for another reason", markedDead, failure, nil},
		{"repack context, cancelled, no error", markedDead, nil, nil},
	} {
		if got := asyncSaveResult(tc.ctx, tc.err); got != tc.want {
			t.Errorf("%s: the uploader's group gets %v, want %v", tc.name, got, tc.want)
		}
	}
}
