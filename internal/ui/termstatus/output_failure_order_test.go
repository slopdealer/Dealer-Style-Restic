package termstatus

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

type outputFailureSequenceWriter struct {
	mu     *sync.Mutex
	events *[]string
	data   []byte
	calls  int
}

func (w *outputFailureSequenceWriter) Write(p []byte) (int, error) {
	w.calls++
	switch w.calls {
	case 1:
		n := len(p) / 2
		w.mu.Lock()
		w.data = append(w.data, p[:n]...)
		*w.events = append(*w.events, "stdout:"+string(p[:n]))
		w.mu.Unlock()
		return n, errors.New("temporary partial write error")
	case 2:
		return 0, errors.New("temporary zero-progress write error")
	case 3:
		return 0, nil
	default:
		w.mu.Lock()
		w.data = append(w.data, p...)
		*w.events = append(*w.events, "stdout:"+string(p))
		w.mu.Unlock()
		return len(p), nil
	}
}

type outputFailureRecordingWriter struct {
	mu     *sync.Mutex
	name   string
	events *[]string
	data   []byte
}

func (w *outputFailureRecordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data = append(w.data, p...)
	*w.events = append(*w.events, w.name+":"+string(p))
	return len(p), nil
}

func TestOutputRetryKeepsSuffixAheadOfLaterStdoutAndStderr(t *testing.T) {
	var mu sync.Mutex
	var events []string
	stdout := &outputFailureSequenceWriter{mu: &mu, events: &events}
	stderr := &outputFailureRecordingWriter{mu: &mu, name: "stderr", events: &events}
	term := new(nil, stdout, stderr, true)
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	term.Print("first")
	term.Flush()
	term.Print("later stdout")
	term.Error("later stderr")
	term.Flush()
	cancel()
	<-term.closed

	rtest.Equals(t, "first\n", string(stdout.data))
	rtest.Equals(t, []string{
		"stdout:fir",
		"stdout:st\n",
		"stderr:write failed: temporary partial write error\n",
		"stderr:write failed: temporary zero-progress write error\n",
		"stderr:write failed: short write\n",
		"stdout:later stdout\n",
		"stderr:later stderr\n",
	}, events)
}

func TestOutputRetryDoesNotMergeLaterStdoutIntoStartedBatch(t *testing.T) {
	var mu sync.Mutex
	var events []string
	stdout := &outputFailureSequenceWriter{mu: &mu, events: &events}
	stderr := &outputFailureRecordingWriter{mu: &mu, name: "stderr", events: &events}
	term := new(nil, stdout, stderr, true)
	term.batchStdout = true
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	writer := term.OutputWriter()
	_, err := writer.Write([]byte("first\n"))
	rtest.OK(t, err)
	term.Flush()
	_, err = writer.Write([]byte("later stdout\n"))
	rtest.OK(t, err)
	term.Flush()
	term.Error("later stderr")
	term.Flush()
	cancel()
	<-term.closed

	rtest.Equals(t, "first\nlater stdout\n", string(stdout.data))
	rtest.Equals(t, []string{
		"stdout:fir",
		"stdout:st\n",
		"stderr:write failed: temporary partial write error\n",
		"stderr:write failed: temporary zero-progress write error\n",
		"stderr:write failed: short write\n",
		"stdout:later stdout\n",
		"stderr:later stderr\n",
	}, events)
}

type outputFailureStalledWriter struct {
	calls int
}

func (w *outputFailureStalledWriter) Write([]byte) (int, error) {
	w.calls++
	return 0, nil
}

type outputFailurePartialThenStalledWriter struct {
	calls int
	data  []byte
}

func (w *outputFailurePartialThenStalledWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		n := len(p) / 2
		w.data = append(w.data, p[:n]...)
		return n, errors.New("partial write before shutdown stall")
	}
	return 0, nil
}

func TestOutputShutdownReportsPersistentZeroProgress(t *testing.T) {
	stdout := &outputFailureStalledWriter{}
	var stderr bytes.Buffer
	term, shutdown := Setup(nil, stdout, &stderr, true)

	writerProvider, ok := term.(interface{ OutputWriter() io.Writer })
	if !ok {
		t.Fatal("terminal does not provide an output writer")
	}
	_, err := writerProvider.OutputWriter().Write([]byte("buffered tail"))
	rtest.OK(t, err)
	shutdown()

	rtest.Equals(t, 3, stdout.calls)
	rtest.Equals(t, 3, strings.Count(stderr.String(), "write failed: short write\n"))
}

func TestOutputShutdownReportsStalledSuffixAfterPartialWrite(t *testing.T) {
	stdout := &outputFailurePartialThenStalledWriter{}
	var stderr bytes.Buffer
	term, shutdown := Setup(nil, stdout, &stderr, true)

	writerProvider, ok := term.(interface{ OutputWriter() io.Writer })
	if !ok {
		t.Fatal("terminal does not provide an output writer")
	}
	_, err := writerProvider.OutputWriter().Write([]byte("buffered tail\n"))
	rtest.OK(t, err)
	shutdown()

	rtest.Equals(t, 4, stdout.calls)
	rtest.Equals(t, "buffere", string(stdout.data))
	rtest.Equals(t, 1, strings.Count(stderr.String(), "write failed: partial write before shutdown stall\n"))
	rtest.Equals(t, 3, strings.Count(stderr.String(), "write failed: short write\n"))
}
