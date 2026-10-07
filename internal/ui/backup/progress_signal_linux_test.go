//go:build linux

package backup

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/restic/restic/internal/archiver"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/ui/signals"
)

type signalProgressSnapshot struct {
	total, processed Counter
	errors           uint
}

type signalProgressPrinter struct {
	*mockPrinter
	updates chan signalProgressSnapshot
}

func (p *signalProgressPrinter) Update(total, processed Counter, errors uint, _ map[string]struct{}, _ time.Time, _ uint64) {
	p.updates <- signalProgressSnapshot{total, processed, errors}
}

// Signals are process-wide and the updater channel has a single consumer.
// Use a child containing just this test so another test cannot consume it.
func TestProgressSignalWithoutPeriodicUpdates(t *testing.T) {
	const childEnv = "RESTIC_TEST_PROGRESS_SIGNAL_CHILD"
	if os.Getenv(childEnv) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestProgressSignalWithoutPeriodicUpdates$", "-test.v", "-test.timeout=20s")
		cmd.Env = append(os.Environ(), childEnv+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("signal child: %v\n%s", err, output)
		}
		return
	}

	printer := &signalProgressPrinter{
		mockPrinter: &mockPrinter{Printer: restic.NewNoopPrinter()},
		updates:     make(chan signalProgressSnapshot, 4),
	}
	// Quiet mode has no ticker, but must still account for signal reports.
	p := NewProgress(printer, true, false, false)
	defer p.Finish(restic.ID{}, nil, false)
	_ = signals.GetProgressChannel() // Install the signal handler before sending.
	assertSignal := func(want signalProgressSnapshot) {
		t.Helper()
		if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-printer.updates:
			if got != want {
				t.Fatalf("signal snapshot=%+v, want %+v", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("signal did not trigger progress without a ticker")
		}
	}

	p.CompleteBlob(17)
	p.CompleteItem("file", "", archiver.ItemStats{}, 0)
	assertSignal(signalProgressSnapshot{processed: Counter{Bytes: 17}})
	p.ReportTotal("", archiver.ScanStats{Files: 1, Bytes: 17})
	p.CompleteItem("file", archiver.ActionFileUnchanged, archiver.ItemStats{}, 0)
	assertSignal(signalProgressSnapshot{total: Counter{Files: 1, Bytes: 17}, processed: Counter{Files: 1, Bytes: 17}})
}
