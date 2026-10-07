package backup

import (
	"reflect"
	"testing"
	"time"

	"github.com/restic/restic/internal/archiver"
	"github.com/restic/restic/internal/ui"
)

// completeItemStock is stock's textProgress.CompleteItem: every argument is
// formatted, whatever the verbosity, and VV decides whether a line appears.
func completeItemStock(b *textProgress, messageType, item string, s archiver.ItemStats, d time.Duration) {
	item = ui.Quote(item)

	switch messageType {
	case "dir new":
		b.VV("new       %v, saved in %.3fs (%v added, %v stored, %v metadata)",
			item, d.Seconds(), ui.FormatBytes(s.DataSize),
			ui.FormatBytes(s.DataSizeInRepo), ui.FormatBytes(s.TreeSizeInRepo))
	case "dir unchanged":
		b.VV("unchanged %v", item)
	case "dir modified":
		b.VV("modified  %v, saved in %.3fs (%v added, %v stored, %v metadata)",
			item, d.Seconds(), ui.FormatBytes(s.DataSize),
			ui.FormatBytes(s.DataSizeInRepo), ui.FormatBytes(s.TreeSizeInRepo))
	case "file new":
		b.VV("new       %v, saved in %.3fs (%v added, %v stored)", item,
			d.Seconds(), ui.FormatBytes(s.DataSize), ui.FormatBytes(s.DataSizeInRepo))
	case "file unchanged":
		b.VV("unchanged %v", item)
	case "file modified":
		b.VV("modified  %v, saved in %.3fs (%v added, %v stored)", item,
			d.Seconds(), ui.FormatBytes(s.DataSize), ui.FormatBytes(s.DataSizeInRepo))
	}
}

// CompleteItem returns early below verbosity 3. What reaches the terminal
// must be what stock prints at every verbosity, for every kind of item.
func TestCompleteItemPrintsWhatStockPrints(t *testing.T) {
	kinds := []string{"dir new", "dir unchanged", "dir modified", "file new", "file unchanged", "file modified", "unknown kind"}
	items := []string{"/plain/path", "/with space/ü", "/control\ncharacter", "/invalid\xffutf8", ""}
	stats := archiver.ItemStats{DataSize: 12345, DataSizeInRepo: 6789, TreeSize: 400, TreeSizeInRepo: 222}

	printedLines := 0
	for verbosity := uint(0); verbosity <= 4; verbosity++ {
		gotTerm, wantTerm := &ui.MockTerminal{}, &ui.MockTerminal{}
		got := NewTextProgress(gotTerm, verbosity).(*textProgress)
		want := NewTextProgress(wantTerm, verbosity).(*textProgress)
		for _, kind := range kinds {
			for _, item := range items {
				got.CompleteItem(kind, item, stats, 1500*time.Millisecond)
				completeItemStock(want, kind, item, stats, 1500*time.Millisecond)
			}
		}
		if !reflect.DeepEqual(gotTerm.Output, wantTerm.Output) || !reflect.DeepEqual(gotTerm.Errors, wantTerm.Errors) {
			t.Errorf("verbosity %d: printed\n%q\nstock prints\n%q", verbosity, gotTerm.Output, wantTerm.Output)
		}
		if verbosity < 3 && len(wantTerm.Output) != 0 {
			t.Errorf("verbosity %d: stock prints %d item lines; the early return would drop them", verbosity, len(wantTerm.Output))
		}
		printedLines += len(gotTerm.Output)
	}
	// 6 known kinds x 5 items at verbosity 3 and 4
	if printedLines != 2*6*len(items) {
		t.Errorf("%d item lines printed at verbosity 3 and 4, want %d", printedLines, 2*6*len(items))
	}
}
