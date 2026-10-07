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

type retryOrderWriter struct {
	mu        *sync.Mutex
	name      string
	events    *[]string
	data      []byte
	failFirst bool
}

func (w *retryOrderWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n := len(p)
	var err error
	if w.failFirst {
		n = len(p) / 2
		w.failFirst = false
		err = errors.New("temporary partial write error")
	}
	w.data = append(w.data, p[:n]...)
	*w.events = append(*w.events, w.name+":"+string(p[:n]))
	return n, err
}

func TestOutputRetryKeepsStdoutAheadOfLaterStderr(t *testing.T) {
	var mu sync.Mutex
	var events []string
	stdout := &retryOrderWriter{mu: &mu, name: "stdout", events: &events, failFirst: true}
	stderr := &retryOrderWriter{mu: &mu, name: "stderr", events: &events}
	term := new(nil, stdout, stderr, true)
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	term.Print("first")
	term.Error("second")
	term.Flush()
	cancel()
	<-term.closed

	rtest.Equals(t, "first\n", string(stdout.data))
	rtest.Equals(t, "write failed: temporary partial write error\nsecond\n", string(stderr.data))
	rtest.Equals(t, []string{
		"stdout:fir",
		"stdout:st\n",
		"stderr:write failed: temporary partial write error\n",
		"stderr:second\n",
	}, events)
}

type stalledOutputWriter struct {
	calls int
}

func (w *stalledOutputWriter) Write([]byte) (int, error) {
	w.calls++
	return 0, nil
}

func TestSetupShutdownReportsStalledOutput(t *testing.T) {
	stdout := &stalledOutputWriter{}
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

var _ io.Writer = &stalledOutputWriter{}
