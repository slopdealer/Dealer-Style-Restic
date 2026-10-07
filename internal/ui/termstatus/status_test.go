package termstatus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tty "github.com/restic/restic/internal/terminal"
	rtest "github.com/restic/restic/internal/test"
)

func TestSetStatus(t *testing.T) {
	buf, term, cancel := setupStatusTest()

	const (
		cl   = tty.PosixControlClearLine
		home = tty.PosixControlMoveCursorHome
		up   = tty.PosixControlMoveCursorUp

		clearLn = home + cl
	)

	term.SetStatus([]string{"first"})
	exp := clearLn + "first" + home

	term.SetStatus([]string{""})
	exp += clearLn + "" + home

	term.SetStatus([]string{})
	exp += clearLn + "" + home

	// already empty status
	term.SetStatus([]string{})

	term.SetStatus([]string{"foo", "bar", "baz"})
	exp += clearLn + "foo\n" + clearLn + "bar\n" + clearLn + "baz" + home + up + up

	term.SetStatus([]string{"quux", "needs\nquote"})
	exp += clearLn + "quux\n" +
		clearLn + "\"needs\\nquote\"\n" +
		clearLn + home + up + up // Clear third line

	cancel()
	exp += clearLn + "\n" + clearLn + "" + home + up // Status cleared

	<-term.closed
	rtest.Equals(t, exp, buf.String())
}

func TestSetStatusUnchangedLines(t *testing.T) {
	buf, term, cancel := setupStatusTest()

	const (
		cl   = tty.PosixControlClearLine
		home = tty.PosixControlMoveCursorHome
		up   = tty.PosixControlMoveCursorUp
		down = tty.PosixControlMoveCursorDown

		clearLn  = home + cl
		stepDown = home + down
	)

	term.SetStatus([]string{"line1", "line2", "line3"})
	exp := clearLn + "line1\n" + clearLn + "line2\n" + clearLn + "line3" + home + up + up

	term.SetStatus([]string{"line1", "line2", "line3-changed"})
	exp += stepDown + stepDown + clearLn + "line3-changed" + home + up + up

	term.SetStatus([]string{"line1", "line2", "line3-changed"})

	term.SetStatus([]string{"line1", "line2-new", "line3-changed"})
	exp += stepDown + clearLn + "line2-new\n" + home + up + up

	cancel()
	exp += clearLn + "\n" + clearLn + "\n" + clearLn + "" + home + up + up

	<-term.closed
	rtest.Equals(t, exp, buf.String())
}

func setupStatusTest() (*bytes.Buffer, *terminal, context.CancelFunc) {
	buf := &bytes.Buffer{}
	term := new(nil, buf, buf, false)

	term.canUpdateStatus = true
	term.fd = ^uintptr(0)
	term.clearCurrentLine = tty.PosixClearCurrentLine
	term.moveCursorUp = tty.PosixMoveCursorUp
	term.moveCursorDown = tty.PosixMoveCursorDown

	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)
	return buf, term, cancel
}

func TestPrint(t *testing.T) {
	buf, term, cancel := setupStatusTest()

	const (
		cl   = tty.PosixControlClearLine
		home = tty.PosixControlMoveCursorHome
	)

	term.Print("test")
	exp := home + cl + "test\n"
	term.Error("error")
	exp += home + cl + "error\n"

	cancel()

	<-term.closed
	rtest.Equals(t, exp, buf.String())
}

func TestSanitizeLines(t *testing.T) {
	var tests = []struct {
		input  []string
		width  int
		output []string
	}{
		{[]string{""}, 80, []string{""}},
		{[]string{"too long test line"}, 10, []string{"too long"}},
		{[]string{"too long test line", "text"}, 10, []string{"too long", "text"}},
		{[]string{"too long test line", "second long test line"}, 10, []string{"too long", "second l"}},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("%s %d", test.input, test.width), func(t *testing.T) {
			out := sanitizeLines(test.input, test.width)
			rtest.Equals(t, test.output, out)
		})
	}
}

type errorReader struct{ err error }

func (r *errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestReadPassword(t *testing.T) {
	want := errors.New("foo")
	_, err := readPassword(&errorReader{want})
	rtest.Assert(t, errors.Is(err, want), "wrong error %v", err)
}

func TestReadPasswordTerminal(t *testing.T) {
	expected := "password"
	term := new(io.NopCloser(strings.NewReader(expected)), io.Discard, io.Discard, false)
	pw, err := term.ReadPassword(context.Background(), "test")
	rtest.OK(t, err)
	rtest.Equals(t, expected, pw)
}

func TestRawInputOutput(t *testing.T) {
	input := io.NopCloser(strings.NewReader("password"))
	var output bytes.Buffer
	term, cancel := Setup(input, &output, io.Discard, false)
	defer cancel()
	rtest.Equals(t, input, term.InputRaw())
	rtest.Equals(t, false, term.InputIsTerminal())
	rtest.Equals(t, io.Writer(&output), term.OutputRaw())
	rtest.Equals(t, false, term.OutputIsTerminal())
	rtest.Equals(t, false, term.CanUpdateStatus())
}

func TestDisableStatus(t *testing.T) {
	var output bytes.Buffer
	term, cancel := Setup(nil, &output, &output, true)
	rtest.Equals(t, false, term.CanUpdateStatus())

	term.Print("test")
	term.Error("error")
	term.SetStatus([]string{"status"})

	cancel()
	rtest.Equals(t, "test\nerror\nstatus\n", output.String())
}

func TestOutputWriter(t *testing.T) {
	var output bytes.Buffer
	term, cancel := Setup(nil, &output, &output, true)

	_, err := term.OutputWriter().Write([]byte("output\npartial"))
	rtest.OK(t, err)
	term.Print("test")
	term.Error("error")

	cancel()
	rtest.Equals(t, "output\ntest\nerror\npartial\n", output.String())
}

func TestFlushWaitsForPendingOutput(t *testing.T) {
	var output bytes.Buffer
	term, cancel := Setup(nil, &output, &output, true)
	defer cancel()

	term.Print("stdout")
	term.Error("stderr")
	_, err := term.OutputWriter().Write([]byte("record\n"))
	rtest.OK(t, err)

	term.Flush()
	rtest.Equals(t, "stdout\nstderr\nrecord\n", output.String())
}

type outputRecordWriter struct {
	name   string
	mu     *sync.Mutex
	events *[]string
	data   []byte
	writes int
}

func (w *outputRecordWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data = append(w.data, p...)
	*w.events = append(*w.events, w.name+":"+string(p))
	w.writes++
	return len(p), nil
}

func TestRunWithoutStatusBatchesAndPreservesOrder(t *testing.T) {
	var mu sync.Mutex
	var events []string
	stdout := &outputRecordWriter{name: "stdout", mu: &mu, events: &events}
	stderr := &outputRecordWriter{name: "stderr", mu: &mu, events: &events}
	term := new(nil, stdout, stderr, true)
	term.batchStdout = true
	term.batchStderr = true
	term.outputBatchWindow = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	term.Print("first")
	term.Print("second")
	term.Flush()
	term.Error("problem")
	term.Print("last")
	term.SetStatus([]string{"status"})
	_, err := term.OutputWriter().Write([]byte("tail"))
	rtest.OK(t, err)
	rtest.OK(t, term.outputWriter.Close())
	term.Flush()
	cancel()
	<-term.closed

	rtest.Equals(t, "first\nsecond\nlast\nstatus\ntail\n", string(stdout.data))
	rtest.Equals(t, "problem\n", string(stderr.data))
	rtest.Equals(t, []string{
		"stdout:first\nsecond\n",
		"stderr:problem\n",
		"stdout:last\n",
		"stdout:status\n",
		"stdout:tail\n",
	}, events)
	rtest.Equals(t, 4, stdout.writes)
	rtest.Equals(t, 1, stderr.writes)
}

func TestRunWithoutStatusFlushesBatchOnCancel(t *testing.T) {
	var output strings.Builder
	term := new(nil, &output, io.Discard, true)
	term.batchStdout = true
	term.outputBatchWindow = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	term.Print("pending")
	cancel()
	<-term.closed
	rtest.Equals(t, "pending\n", output.String())
}

func TestRunWithoutStatusFlushesBatchWhenIdle(t *testing.T) {
	var mu sync.Mutex
	var events []string
	stdout := &outputRecordWriter{name: "stdout", mu: &mu, events: &events}
	term := new(nil, stdout, io.Discard, true)
	term.batchStdout = true
	term.outputBatchWindow = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	term.Print("idle")
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		writtenNow := stdout.writes
		mu.Unlock()
		if writtenNow != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending output was not flushed while idle")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-term.closed
	rtest.Equals(t, "idle\n", string(stdout.data))
}

func TestRunWithoutStatusFlushesBeforeOutputRaw(t *testing.T) {
	var mu sync.Mutex
	var events []string
	stdout := &outputRecordWriter{name: "stdout", mu: &mu, events: &events}
	term := new(nil, stdout, io.Discard, true)
	term.batchStdout = true
	term.outputBatchWindow = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	term.Print("queued")
	raw := term.OutputRaw()
	_, err := io.WriteString(raw, "raw\n")
	rtest.OK(t, err)
	cancel()
	<-term.closed
	rtest.Equals(t, "queued\nraw\n", string(stdout.data))
	rtest.Equals(t, []string{"stdout:queued\n", "stdout:raw\n"}, events)
}

func TestRunWithoutStatusKeepsCustomWriterCallsSeparate(t *testing.T) {
	w := &shortOutputWriter{}
	term := new(nil, w, io.Discard, true)
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	term.Print("one")
	term.Print("two")
	term.Flush()
	cancel()
	<-term.closed
	rtest.Equals(t, []string{"one\n", "two\n"}, w.calls)
	rtest.Equals(t, "ot", string(w.data))
}

type shortOutputWriter struct {
	calls []string
	data  []byte
}

func (w *shortOutputWriter) Write(p []byte) (int, error) {
	w.calls = append(w.calls, string(p))
	if len(p) == 0 {
		return 0, nil
	}
	w.data = append(w.data, p[0])
	return 1, nil
}

func TestRunWithoutStatusRespectsBatchSizeLimit(t *testing.T) {
	var mu sync.Mutex
	var events []string
	stdout := &outputRecordWriter{name: "stdout", mu: &mu, events: &events}
	term := new(nil, stdout, io.Discard, true)
	term.batchStdout = true
	term.outputBatchWindow = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	term.Print(strings.Repeat("a", maxOutputBatchSize-1))
	term.Print("b")
	term.Flush()
	cancel()
	<-term.closed
	rtest.Equals(t, maxOutputBatchSize+2, len(stdout.data))
	rtest.Equals(t, 2, stdout.writes)
	rtest.Equals(t, byte('\n'), stdout.data[maxOutputBatchSize-1])
	rtest.Equals(t, "b\n", string(stdout.data[maxOutputBatchSize:]))
	rtest.Equals(t, maxOutputBatchSize, len(events[0])-len("stdout:"))
	rtest.Equals(t, "stdout:b\n", events[1])
}

type partialErrorOutputWriter struct {
	data  []byte
	calls int
}

func (w *partialErrorOutputWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		n := len(p) / 2
		w.data = append(w.data, p[:n]...)
		return n, errors.New("temporary write error")
	}
	w.data = append(w.data, p...)
	return len(p), nil
}

func TestRunWithoutStatusRetriesPartialBatchWrite(t *testing.T) {
	w := &partialErrorOutputWriter{}
	term := new(nil, w, io.Discard, true)
	term.batchStdout = true
	term.outputBatchWindow = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	term.Print("one")
	term.Print("two")
	term.Flush()
	cancel()
	<-term.closed
	rtest.Equals(t, "one\ntwo\n", string(w.data))
	rtest.Equals(t, 2, w.calls)
}

type recoveringOutputWriter struct {
	data  []byte
	calls int
}

func (w *recoveringOutputWriter) Write(p []byte) (int, error) {
	w.calls++
	switch w.calls {
	case 1:
		n := len(p) / 2
		w.data = append(w.data, p[:n]...)
		return n, errors.New("temporary partial write error")
	case 2:
		return 0, errors.New("temporary zero-progress write error")
	default:
		w.data = append(w.data, p...)
		return len(p), nil
	}
}

func TestRunWithoutStatusKeepsSuffixAfterZeroProgressRetry(t *testing.T) {
	w := &recoveringOutputWriter{}
	term := new(nil, w, io.Discard, true)
	term.batchStdout = true
	term.outputBatchWindow = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	term.Print("one")
	term.Print("two")
	term.Flush()
	rtest.Equals(t, "one\n", string(w.data))
	rtest.Equals(t, 2, w.calls)

	term.Print("three")
	term.Flush()
	cancel()
	<-term.closed
	rtest.Equals(t, "one\ntwo\nthree\n", string(w.data))
	rtest.Equals(t, 3, w.calls)
}

func TestRunWithoutStatusWritesOversizedRecordDirectly(t *testing.T) {
	var mu sync.Mutex
	var events []string
	stdout := &outputRecordWriter{name: "stdout", mu: &mu, events: &events}
	term := new(nil, stdout, io.Discard, true)
	term.batchStdout = true
	term.outputBatchWindow = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	line := strings.Repeat("x", maxOutputBatchSize+1)
	term.Print(line)
	term.Flush()
	cancel()
	<-term.closed
	rtest.Equals(t, line+"\n", string(stdout.data))
	rtest.Equals(t, 1, stdout.writes)
}
