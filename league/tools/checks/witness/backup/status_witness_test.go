package termstatus

// WITNESS (review of S019-merge, area backup / user-visible output): the output queue in
// runWithoutStatus (used whenever stdout is not an interactive terminal, i.e. cron jobs,
// pipes, redirected files, --quiet).
//
// TestWitnessStderrNotBlockedByFailingStdout: stdout cannot be written (disk holding the
//   redirected log is full: Write returns 0, ENOSPC). Stock writes every message
//   independently, so error messages still reach stderr. The candidate keeps the failed
//   stdout record at the head of one shared queue and refuses to write anything behind it,
//   so NOTHING printed afterwards reaches stderr, not even at shutdown. FAILS on candidate.
// TestWitnessFailedOutputIsRetainedWithoutBound: while stdout is failing the candidate
//   keeps every later message in memory (stock drops them). Shown by letting the writer
//   recover: all lines printed during the outage are delivered afterwards. FAILS on candidate.
//
// NOTE: the candidate's own internal/ui/termstatus/status_test.go does not compile
// (line 207: term.Flush undefined on ui.Terminal). To run this file the scratch copy was
// patched to term.(*terminal).Flush(); nothing else was changed.
//
// OS: any. Run from the candidate tree (after that one-line patch):
//   go test -run 'TestWitness' -count=1 -p 2 ./internal/ui/termstatus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type failingWriter struct {
	mu      sync.Mutex
	failing bool
	data    bytes.Buffer
	calls   int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.failing {
		return 0, errors.New("no space left on device")
	}
	return w.data.Write(p)
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *lockedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *lockedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestWitnessStderrNotBlockedByFailingStdout(t *testing.T) {
	stdout := &failingWriter{failing: true}
	stderr := &lockedBuffer{}
	term := new(nil, stdout, stderr, true)
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	term.Print("snapshot 1234 saved")                       // stdout: cannot be written
	term.Error("error: open /etc/shadow: permission denied") // stderr: healthy
	term.Error("Fatal: unable to save snapshot")
	term.Flush()
	cancel()
	<-term.closed

	got := stderr.String()
	t.Logf("stderr received:\n%s", got)
	for _, want := range []string{"permission denied", "unable to save snapshot"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr message %q was never written because an undeliverable stdout record is queued in front of it (stock writes it)", want)
		}
	}
}

func TestWitnessFailedOutputIsRetainedWithoutBound(t *testing.T) {
	stdout := &failingWriter{failing: true}
	stderr := &lockedBuffer{}
	term := new(nil, stdout, stderr, true)
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	const lines = 20000
	line := strings.Repeat("v", 100)
	for i := range lines {
		term.Print(fmt.Sprintf("%06d %s", i, line))
	}
	term.Flush()
	stdout.mu.Lock()
	stdout.failing = false // the disk has space again
	stdout.mu.Unlock()
	term.Print("after outage")
	term.Flush()
	cancel()
	<-term.closed

	stdout.mu.Lock()
	delivered := stdout.data.Len()
	stdout.mu.Unlock()
	t.Logf("bytes delivered to stdout after the outage: %d (the single line printed after recovery is %d bytes)", delivered, len("after outage\n"))
	if delivered > 10*len("after outage\n") {
		t.Errorf("candidate retained %d bytes (%d lines) of undeliverable output in memory during the outage; stock drops failed lines, so retention is unbounded for a long verbose run", delivered, delivered/108)
	}
}
