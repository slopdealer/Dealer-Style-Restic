package termstatus

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	rtest "github.com/restic/restic/internal/test"
)

type batchTestWriter struct {
	mu     sync.Mutex
	data   []byte
	writes int
}

func (w *batchTestWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data = append(w.data, p...)
	w.writes++
	return len(p), nil
}

func TestOutputWriterBatchesCompleteLinesBeforeOtherTerminalEvents(t *testing.T) {
	term := new(nil, io.Discard, io.Discard, true)
	term.batchStdout = true
	term.outputBatchWindow = time.Hour
	term.msg = make(chan message, 4)
	term.status = make(chan status, 1)

	writer := term.OutputWriter()
	_, err := writer.Write([]byte("first\n"))
	rtest.OK(t, err)
	_, err = writer.Write([]byte("second\n"))
	rtest.OK(t, err)
	term.Error("problem")
	term.SetStatus([]string{"status"})
	rtest.OK(t, writer.(io.Closer).Close())

	first := <-term.msg
	rtest.Equals(t, "first\nsecond\n", first.line)
	rtest.Equals(t, false, first.err)
	second := <-term.msg
	rtest.Equals(t, "problem\n", second.line)
	rtest.Equals(t, true, second.err)
	rtest.Equals(t, status{lines: []string{"status"}}, <-term.status)
}

func TestOutputWriterBatchesCompleteLinesWhenIdle(t *testing.T) {
	stdout := &batchTestWriter{}
	term := new(nil, stdout, io.Discard, true)
	term.batchStdout = true
	term.outputBatchWindow = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	writer := term.OutputWriter()
	_, err := writer.Write([]byte("first\n"))
	rtest.OK(t, err)
	_, err = writer.Write([]byte("second\n"))
	rtest.OK(t, err)

	deadline := time.Now().Add(time.Second)
	for {
		stdout.mu.Lock()
		writes := stdout.writes
		stdout.mu.Unlock()
		if writes > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("complete lines were not flushed while idle")
		}
		time.Sleep(time.Millisecond)
	}

	rtest.OK(t, writer.(io.Closer).Close())
	term.Flush()
	cancel()
	<-term.closed

	stdout.mu.Lock()
	data := string(stdout.data)
	writes := stdout.writes
	stdout.mu.Unlock()
	rtest.Equals(t, "first\nsecond\n", data)
	rtest.Equals(t, 1, writes)
}
