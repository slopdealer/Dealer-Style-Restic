package termstatus

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// boundaryWriter fails its first Write after taking limit bytes of it and
// works from then on.
type boundaryWriter struct {
	bytes.Buffer
	first bool
	limit int
}

func (w *boundaryWriter) Write(p []byte) (int, error) {
	if !w.first {
		w.first = true
		n := min(w.limit, len(p))
		_, _ = w.Buffer.Write(p[:n])
		return n, errors.New("injected disk-full error")
	}
	return w.Buffer.Write(p)
}

// TestBatchErrorAtMessageBoundary is the red team's test (seat 3, finding 3):
// a write error that lands exactly on a message boundary must not cost the
// next message, which stock writes.
func TestBatchErrorAtMessageBoundary(t *testing.T) {
	const first = "first\n"
	const second = "second\n"
	stock := &boundaryWriter{limit: len(first)}
	// The exact upstream runWithoutStatus operations for two Print calls.
	_, _ = io.WriteString(struct{ io.Writer }{stock}, first)
	_, _ = io.WriteString(struct{ io.Writer }{stock}, second)
	if stock.String() != first+second {
		t.Fatalf("invalid upstream control: %q", stock.String())
	}
	modified := &boundaryWriter{limit: len(first)}
	var stderr bytes.Buffer
	term := new(nil, modified, &stderr, true)
	term.writeBatch(modified, []byte(first+second), []int{len(first), len(first) + len(second)})
	if modified.String() != stock.String() {
		t.Fatalf("upstream writes %q; batching writes %q; diagnostics=%q", stock.String(), modified.String(), stderr.String())
	}
}

// offsetWriter fails the Write that reaches its limit, after taking the bytes
// up to the limit, and works from then on.
type offsetWriter struct {
	bytes.Buffer
	tripped bool
	limit   int
}

func (w *offsetWriter) Write(p []byte) (int, error) {
	if !w.tripped && w.Len()+len(p) >= w.limit {
		w.tripped = true
		n := w.limit - w.Len()
		_, _ = w.Buffer.Write(p[:n])
		return n, errors.New("injected disk-full error")
	}
	return w.Buffer.Write(p)
}

// TestBatchErrorFollowsMessagesNotLines compares the batch with stock's one
// write per message for every offset at which the output can fail, with
// messages that hold more than one line: what is written and what is reported
// must be the same.
func TestBatchErrorFollowsMessagesNotLines(t *testing.T) {
	messages := []string{"one\n", "two\nstill two\n", "three\n", "\n", "five\nfive\nfive\n"}
	var batch []byte
	var ends []int
	for _, msg := range messages {
		batch = append(batch, msg...)
		ends = append(ends, len(batch))
	}
	for limit := 0; limit <= len(batch); limit++ {
		stock := &offsetWriter{limit: limit}
		var stockErr bytes.Buffer
		stockTerm := new(nil, stock, &stockErr, true)
		for _, msg := range messages {
			// the upstream runWithoutStatus operations for one Print call
			_, err := io.WriteString(struct{ io.Writer }{stock}, msg)
			stockTerm.logWriteErr(err)
		}

		modified := &offsetWriter{limit: limit}
		var stderr bytes.Buffer
		term := new(nil, modified, &stderr, true)
		term.writeBatch(modified, batch, ends)
		if modified.String() != stock.String() {
			t.Fatalf("failure at byte %d: stock writes %q; batching writes %q", limit, stock.String(), modified.String())
		}
		if stderr.String() != stockErr.String() {
			t.Fatalf("failure at byte %d: stock reports %q; batching reports %q", limit, stockErr.String(), stderr.String())
		}
	}
}

// A disk that fills up fails every write from the byte at which it is full.
// For every such byte the batch must leave the file and the reports of stock,
// which makes one write per message.
func TestBatchOnFullDiskFollowsMessages(t *testing.T) {
	messages := []string{"one\n", "two\nstill two\n", "three\n", "\n", "five\nfive\nfive\n"}
	var batch []byte
	var ends []int
	for _, msg := range messages {
		batch = append(batch, msg...)
		ends = append(ends, len(batch))
	}
	for free := 0; free <= len(batch); free++ {
		stock := &quotaWriter{free: free}
		var stockErr bytes.Buffer
		stockTerm := new(nil, stock, &stockErr, true)
		for _, msg := range messages {
			_, err := io.WriteString(struct{ io.Writer }{stock}, msg)
			stockTerm.logWriteErr(err)
		}

		modified := &quotaWriter{free: free}
		var stderr bytes.Buffer
		term := new(nil, modified, &stderr, true)
		term.writeBatch(modified, batch, ends)
		if !bytes.Equal(modified.data, stock.data) {
			t.Fatalf("%d bytes free: stock leaves %q; batching leaves %q", free, stock.data, modified.data)
		}
		if stderr.String() != stockErr.String() {
			t.Fatalf("%d bytes free: stock reports %q; batching reports %q", free, stockErr.String(), stderr.String())
		}
	}
}

// The complete lines of one Write to the output writer are one message in
// stock. Collected with other messages they stay one: a write that stops in
// its first line loses its second line too, and the next message is written.
func TestOutputWriterKeepsMessageOfOneWrite(t *testing.T) {
	const first, second = "a1\na2\n", "b\n"
	for limit := 0; limit <= len(first)+len(second); limit++ {
		stock := &offsetWriter{limit: limit}
		_, _ = io.WriteString(struct{ io.Writer }{stock}, first)
		_, _ = io.WriteString(struct{ io.Writer }{stock}, second)

		modified := &offsetWriter{limit: limit}
		var stderr bytes.Buffer
		term := new(nil, modified, &stderr, true)
		term.batchStdout = true
		term.outputBatchWindow = time.Hour
		ctx, cancel := context.WithCancel(context.Background())
		go term.Run(ctx)
		_, _ = term.OutputWriter().Write([]byte(first))
		_, _ = term.OutputWriter().Write([]byte(second))
		term.Flush()
		cancel()
		<-term.closed
		if modified.String() != stock.String() {
			t.Fatalf("failure at byte %d: stock writes %q; the collected output is %q", limit, stock.String(), modified.String())
		}
	}
}
