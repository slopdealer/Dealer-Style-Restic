package backup

import (
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/archiver"
	"github.com/restic/restic/internal/restic"
)

// An ordinary scanner publication must not wait for archive accounting.
func TestProgressScanPublicationIndependent(t *testing.T) {
	printer := &mockPrinter{Printer: restic.NewNoopPrinter()}
	p := newProgress(printer, 0)
	defer p.Finish(restic.ID{}, nil, false)

	p.mu.Lock()
	done := make(chan struct{})
	go func() {
		p.ReportTotal("file", archiver.ScanStats{Files: 1, Bytes: 17})
		close(done)
	}()
	select {
	case <-done:
		p.mu.Unlock()
	case <-time.After(5 * time.Second):
		p.mu.Unlock()
		<-done
		t.Fatal("scanner publication waited for archive accounting")
	}
}

type gatedScanPrinter struct {
	*mockPrinter
	entered, release chan struct{}
}

func (p *gatedScanPrinter) ReportTotal(_ time.Time, _ archiver.ScanStats) {
	close(p.entered)
	<-p.release
}

// Final publication still excludes accounting and status updates until the
// printer has consumed the final scan result.
func TestProgressFinalScanPublicationSerialized(t *testing.T) {
	printer := &gatedScanPrinter{
		mockPrinter: &mockPrinter{Printer: restic.NewNoopPrinter()},
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	p := newProgress(printer, 0)
	done := make(chan struct{})
	go func() {
		p.ReportTotal("", archiver.ScanStats{Files: 1, Bytes: 17})
		close(done)
	}()
	select {
	case <-printer.entered:
	case <-time.After(5 * time.Second):
		close(printer.release)
		<-done
		p.Finish(restic.ID{}, nil, false)
		t.Fatal("final scan result did not reach the printer")
	}
	if p.mu.TryLock() {
		p.mu.Unlock()
		t.Error("archive accounting can overtake final scan publication")
	}
	if p.scanMu.TryLock() {
		p.scanMu.Unlock()
		t.Error("status updates can overtake final scan publication")
	}
	close(printer.release)
	<-done
	p.Finish(restic.ID{}, nil, false)
}

func TestProgressIndependentScanAndArchiveAccounting(t *testing.T) {
	p := newProgress(&mockPrinter{Printer: restic.NewNoopPrinter()}, 0)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := uint(1); i <= 1000; i++ {
			p.ReportTotal("file", archiver.ScanStats{Files: i, Dirs: 1, Bytes: uint64(i) * 17})
		}
		p.ReportTotal("", archiver.ScanStats{Files: 1000, Dirs: 1, Bytes: 17000})
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			p.StartFile("file")
			p.CompleteBlob(17)
			p.CompleteItem("file", archiver.ItemAction(""), archiver.ItemStats{}, 0)
			p.CompleteItem("file", archiver.ActionFileUnchanged, archiver.ItemStats{}, 0)
		}
		p.CompleteItem("dir", archiver.ActionDirUnchanged, archiver.ItemStats{}, 0)
	}()
	wg.Wait()
	p.Finish(restic.ID{}, nil, false)

	want := Counter{Files: 1000, Dirs: 1, Bytes: 17000}
	if p.total != want || p.processed != want || len(p.currentFiles) != 0 {
		t.Fatalf("scan=%+v archive=%+v current=%v, want %+v", p.total, p.processed, p.currentFiles, want)
	}
	if !p.scanFinished {
		t.Fatal("final scan result was not published")
	}
}
