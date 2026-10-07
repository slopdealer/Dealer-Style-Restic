package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/restic/restic/internal/archiver"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/ui"
	"github.com/restic/restic/internal/ui/backup"
)

type b16ProgressTerminal struct {
	*ui.MockTerminal
	output, errors chan string
}

func (t *b16ProgressTerminal) Print(line string) { t.output <- line }
func (t *b16ProgressTerminal) Error(line string) { t.errors <- line }

// Exercise the public progress path from the command package as well as the
// private lock tests: reading completion, save completion, scanner totals and
// the two error categories must remain distinct.
func TestBackupProgressPublicationSemantics(t *testing.T) {
	term := &b16ProgressTerminal{
		MockTerminal: &ui.MockTerminal{},
		output: make(chan string, 64),
		errors: make(chan string, 8),
	}
	printer := backup.NewJSONProgress(term, 3)
	p := backup.NewProgress(printer, false, true, false)
	defer p.Finish(restic.ID{}, &archiver.Summary{}, false)

	await := func(match func(map[string]any) bool) map[string]any {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case line := <-term.output:
				var event map[string]any
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatal(err)
				}
				if match(event) {
					return event
				}
			case <-timer.C:
				t.Fatal("expected progress event was not emitted")
			}
		}
	}

	p.StartFile("file")
	p.CompleteBlob(17)
	p.CompleteItem("file", "", archiver.ItemStats{}, 0)
	first := await(func(event map[string]any) bool {
		return event["message_type"] == "status" && event["bytes_done"] == float64(17) && event["current_files"] == nil
	})
	if _, ok := first["files_done"]; ok {
		t.Fatal("reading completion counted as successful item completion")
	}
	if _, ok := first["current_files"]; ok {
		t.Fatal("reading completion did not clear the active file")
	}

	if err := printer.ScannerError("scan-path", fmt.Errorf("scan failure")); err != nil {
		t.Fatal(err)
	}
	if err := p.Error("archive-path", fmt.Errorf("archive failure")); err != nil {
		t.Fatal(err)
	}
	for _, during := range []string{"scan", "archival"} {
		select {
		case line := <-term.errors:
			var event map[string]any
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatal(err)
			}
			if event["during"] != during {
				t.Fatalf("error category=%v, want %s", event["during"], during)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("missing %s error", during)
		}
	}

	p.ReportTotal("", archiver.ScanStats{Files: 1, Bytes: 17})
	p.CompleteItem("file", archiver.ActionFileUnchanged, archiver.ItemStats{}, 0)
	last := await(func(event map[string]any) bool {
		return event["message_type"] == "status" && event["files_done"] == float64(1)
	})
	for field, want := range map[string]float64{
		"total_files": 1, "files_done": 1,
		"total_bytes": 17, "bytes_done": 17, "error_count": 1,
	} {
		if last[field] != want {
			t.Errorf("%s=%v, want %v", field, last[field], want)
		}
	}
}
