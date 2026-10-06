package termstatus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	rtest "github.com/restic/restic/internal/test"
)

// What runWithoutStatus does with a writer that fails or makes no progress
// must be what stock does: every message is written once, a failure is
// reported on stderr, nothing is kept and nothing waits for another stream.

type switchableWriter struct {
	mu      sync.Mutex
	failing bool
	data    bytes.Buffer
	calls   int
}

func (w *switchableWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.failing {
		return 0, errors.New("no space left on device")
	}
	return w.data.Write(p)
}

func (w *switchableWriter) setFailing(failing bool) {
	w.mu.Lock()
	w.failing = failing
	w.mu.Unlock()
}

func (w *switchableWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.String()
}

// startTestTerminal runs a terminal without status lines. batch selects the
// collection of messages that is used for regular files.
func startTestTerminal(stdout, stderr *switchableWriter, batch bool) (*terminal, func()) {
	term := new(nil, stdout, stderr, true)
	term.batchStdout = batch
	term.batchStderr = batch
	// collected output is written when a batch is full, on Flush, and when the
	// other stream is used: the tests do not depend on the idle timer
	term.outputBatchWindow = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)
	return term, func() {
		term.Flush()
		cancel()
		<-term.closed
	}
}

func forBothOutputModes(t *testing.T, fn func(t *testing.T, batch bool)) {
	t.Run("one write per message", func(t *testing.T) { fn(t, false) })
	t.Run("regular file", func(t *testing.T) { fn(t, true) })
}

// stdout cannot be written (the disk under a redirected log is full). What is
// printed to stderr afterwards must still arrive there.
func TestStderrIsNotHeldBackByFailingStdout(t *testing.T) {
	forBothOutputModes(t, func(t *testing.T, batch bool) {
		stdout := &switchableWriter{failing: true}
		stderr := &switchableWriter{}
		term, stop := startTestTerminal(stdout, stderr, batch)

		term.Print("snapshot 1234 saved")
		term.Error("error: open /etc/shadow: permission denied")
		term.Print("more output")
		term.Error("Fatal: unable to save snapshot")
		stop()

		rtest.Equals(t, "write failed: no space left on device\n"+
			"error: open /etc/shadow: permission denied\n"+
			"write failed: no space left on device\n"+
			"Fatal: unable to save snapshot\n", stderr.String())
		rtest.Equals(t, "", stdout.String())
	})
}

// While stdout fails nothing is kept in memory: when the writer works again,
// only what is printed from then on is written. Each lost line was reported.
func TestFailedOutputIsNotRetained(t *testing.T) {
	forBothOutputModes(t, func(t *testing.T, batch bool) {
		stdout := &switchableWriter{failing: true}
		stderr := &switchableWriter{}
		term, stop := startTestTerminal(stdout, stderr, batch)

		const lines = 20000
		line := strings.Repeat("v", 100)
		for i := range lines {
			term.Print(fmt.Sprintf("%06d %s", i, line))
		}
		term.Flush()
		stdout.setFailing(false)
		term.Print("after outage")
		stop()

		rtest.Equals(t, "after outage\n", stdout.String())
		// one report per lost line, as in stock, also when lines are collected
		rtest.Equals(t, lines, strings.Count(stderr.String(), "write failed: no space left on device\n"))
		rtest.Equals(t, lines*len("write failed: no space left on device\n"), len(stderr.String()))
	})
}

// A failing stderr does not keep stdout from being written either.
func TestStdoutIsNotHeldBackByFailingStderr(t *testing.T) {
	forBothOutputModes(t, func(t *testing.T, batch bool) {
		stdout := &switchableWriter{}
		stderr := &switchableWriter{failing: true}
		term, stop := startTestTerminal(stdout, stderr, batch)

		term.Error("warning")
		term.Print("one")
		term.Error("warning")
		term.Print("two")
		stop()

		rtest.Equals(t, "one\ntwo\n", stdout.String())
		rtest.Equals(t, "", stderr.String())
	})
}

// scriptedWriter answers every Write from a fixed pseudo-random script and
// records the call in a log shared with the other stream.
type scriptedWriter struct {
	name string
	rnd  *rand.Rand
	log  *[]string
	mu   *sync.Mutex
}

func (w *scriptedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	*w.log = append(*w.log, w.name+":"+string(p))
	switch w.rnd.Intn(8) {
	case 0:
		return 0, errors.New(w.name + " failed")
	case 1:
		return w.rnd.Intn(len(p) + 1), errors.New(w.name + " failed after some bytes")
	case 2:
		// no progress and no error: a stalled writer
		return 0, nil
	case 3:
		return w.rnd.Intn(len(p) + 1), nil
	default:
		return len(p), nil
	}
}

// stockOutputModel is what stock's runWithoutStatus and lineWriter do with a
// sequence of calls: one Write per message, the result ignored apart from an
// error, which is reported with one Write to stderr.
type stockOutputModel struct {
	stdout, stderr *scriptedWriter
	lastStatus     []string
	lineBuf        []byte
}

func (m *stockOutputModel) write(isErr bool, line string) {
	dst := m.stdout
	if isErr {
		dst = m.stderr
	}
	if _, err := dst.Write([]byte(line)); err != nil {
		_, _ = m.stderr.Write([]byte(fmt.Sprintf("write failed: %v\n", err)))
	}
}

func (m *stockOutputModel) print(isErr bool, line string) {
	if len(line) == 0 || line[len(line)-1] != '\n' {
		line += "\n"
	}
	m.write(isErr, line)
}

func (m *stockOutputModel) setStatus(lines []string) {
	lines = sanitizeLines(lines, 0)
	if slices.Equal(lines, m.lastStatus) {
		return
	}
	for _, line := range lines {
		m.write(false, strings.TrimRight(line, "\n")+"\n")
	}
	m.lastStatus = lines
}

func (m *stockOutputModel) outputWrite(data string) {
	m.lineBuf = append(m.lineBuf, data...)
	if i := bytes.LastIndexByte(m.lineBuf, '\n'); i != -1 {
		m.write(false, string(m.lineBuf[:i+1]))
		m.lineBuf = m.lineBuf[i+1:]
	}
}

func (m *stockOutputModel) close() {
	if len(m.lineBuf) > 0 {
		m.write(false, string(m.lineBuf)+"\n")
	}
}

// For writers that are not regular files (pipes, terminals without status
// updates, custom writers) the sequence of Write calls, their arguments and
// their order across both streams equal stock's for any pattern of failures,
// partial writes and writes without progress.
func TestRunWithoutStatusEqualsStockOnFailingWriters(t *testing.T) {
	words := []string{"snapshot", "saved", "error", "open", "permission denied", "", "a\nb", "tail\n", "x"}
	for seed := int64(1); seed <= 300; seed++ {
		ops := rand.New(rand.NewSource(seed))

		var mu sync.Mutex
		var got, want []string
		stdout := &scriptedWriter{name: "stdout", rnd: rand.New(rand.NewSource(seed * 7)), log: &got, mu: &mu}
		stderr := &scriptedWriter{name: "stderr", rnd: rand.New(rand.NewSource(seed * 13)), log: &got, mu: &mu}
		model := &stockOutputModel{
			stdout: &scriptedWriter{name: "stdout", rnd: rand.New(rand.NewSource(seed * 7)), log: &want, mu: &mu},
			stderr: &scriptedWriter{name: "stderr", rnd: rand.New(rand.NewSource(seed * 13)), log: &want, mu: &mu},
		}

		term, shutdown := Setup(nil, stdout, stderr, true)
		for i := 0; i < 60; i++ {
			text := words[ops.Intn(len(words))] + " " + words[ops.Intn(len(words))]
			switch ops.Intn(6) {
			case 0, 1:
				term.Print(text)
				model.print(false, text)
			case 2:
				term.Error(text)
				model.print(true, text)
			case 3:
				lines := []string{"status " + words[ops.Intn(3)], words[ops.Intn(3)]}[:1+ops.Intn(2)]
				term.SetStatus(lines)
				model.setStatus(lines)
			case 4:
				_, err := term.OutputWriter().Write([]byte(text))
				rtest.OK(t, err)
				model.outputWrite(text)
			case 5:
				term.(*terminal).Flush()
			}
		}
		shutdown()
		model.close()

		mu.Lock()
		if len(got) != len(want) {
			t.Fatalf("seed %d: %d writes, stock makes %d\ngot  %q\nwant %q", seed, len(got), len(want), got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("seed %d: write %d is %q, stock writes %q", seed, i, got[i], want[i])
			}
		}
		mu.Unlock()
	}
}

// quotaWriter is a file on a disk that fills up: it takes bytes until the
// quota is used and fails from then on. What it stores does not depend on how
// the bytes are split into Write calls.
type quotaWriter struct {
	mu    sync.Mutex
	free  int
	data  []byte
	calls int
}

func (w *quotaWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if len(p) <= w.free {
		w.free -= len(p)
		w.data = append(w.data, p...)
		return len(p), nil
	}
	n := w.free
	w.free = 0
	w.data = append(w.data, p[:n]...)
	return n, errors.New("no space left on device")
}

// For a regular file the messages are written together. When the disk fills
// up, the file content and the reports on stderr must be those of stock, which
// writes one line at a time: complete lines, then the part of one line that
// still fitted, then one report for that line and one for every later line.
func TestCollectedOutputFailsLikeLineByLineOutput(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261006))
	for round := 0; round < 200; round++ {
		lines := make([]string, 1+rnd.Intn(400))
		total := 0
		for i := range lines {
			lines[i] = fmt.Sprintf("%04d %s\n", i, strings.Repeat("x", rnd.Intn(300)))
			total += len(lines[i])
		}
		quota := rnd.Intn(total + 50)
		flushEvery := 1 + rnd.Intn(len(lines))

		// stock: one write per line
		stock := &quotaWriter{free: quota}
		stockReports := 0
		for _, line := range lines {
			if _, err := stock.Write([]byte(line)); err != nil {
				stockReports++
			}
		}

		stdout := &quotaWriter{free: quota}
		stderr := &switchableWriter{}
		term := new(nil, stdout, stderr, true)
		term.batchStdout = true
		term.outputBatchWindow = time.Hour
		ctx, cancel := context.WithCancel(context.Background())
		go term.Run(ctx)
		for i, line := range lines {
			if round%2 == 0 {
				term.Print(line)
			} else {
				_, err := term.OutputWriter().Write([]byte(line))
				rtest.OK(t, err)
			}
			if i%flushEvery == flushEvery-1 {
				term.Flush()
			}
			if i == len(lines)/2 {
				term.Error("error in the middle")
			}
		}
		term.closeOutputWriter()
		term.Flush()
		cancel()
		<-term.closed

		if !bytes.Equal(stdout.data, stock.data) {
			t.Fatalf("round %d: %d bytes in the file, stock leaves %d", round, len(stdout.data), len(stock.data))
		}
		reports := strings.Count(stderr.String(), "write failed: no space left on device\n")
		if reports != stockReports {
			t.Fatalf("round %d: %d failed writes reported, stock reports %d", round, reports, stockReports)
		}
		if strings.Count(stderr.String(), "error in the middle\n") != 1 {
			t.Fatalf("round %d: the error message did not reach stderr", round)
		}
		if stockReports == 0 && stdout.calls >= len(lines) && len(lines) > 2 && flushEvery > 2 {
			t.Fatalf("round %d: %d writes for %d lines, nothing was collected", round, stdout.calls, len(lines))
		}
	}
}

// A write that stops inside a line gives up that line only, and the writer
// is not called again with bytes it already took.
func TestCollectedOutputPartialWriteDropsOneLine(t *testing.T) {
	var calls []string
	stdout := writerFunc(func(p []byte) (int, error) {
		calls = append(calls, string(p))
		if len(calls) == 1 {
			return len("one\nt"), errors.New("temporary write error")
		}
		return len(p), nil
	})
	stderr := &switchableWriter{}
	term := new(nil, stdout, stderr, true)
	term.batchStdout = true
	term.outputBatchWindow = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	go term.Run(ctx)

	term.Print("one")
	term.Print("two")
	term.Print("three")
	term.Print("four")
	term.Flush()
	cancel()
	<-term.closed

	rtest.Equals(t, []string{"one\ntwo\nthree\nfour\n", "three\n", "four\n"}, calls)
	rtest.Equals(t, "write failed: temporary write error\n", stderr.String())
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
