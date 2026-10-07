package termstatus

import (
	"bytes"
	"io"
	"sync"
	"time"
)

type lineWriter struct {
	m sync.Mutex

	// outputMu serializes writer calls with Print, Error, SetStatus and Flush.
	// A nil outputMu is used by the simple, non-batching constructor.
	outputMu    *sync.Mutex
	batchStdout bool
	idleWindow  time.Duration
	timer       *time.Timer
	lastWrite   time.Time
	batch       []byte
	closed      bool

	buf   bytes.Buffer
	print func(string)
}

var _ io.WriteCloser = &lineWriter{}

func newLineWriter(print func(string)) *lineWriter {
	return &lineWriter{print: print}
}

func newTerminalLineWriter(print func(string), outputMu *sync.Mutex, batchStdout bool, idleWindow time.Duration) *lineWriter {
	return &lineWriter{
		print:       print,
		outputMu:    outputMu,
		batchStdout: batchStdout,
		idleWindow:  idleWindow,
	}
}

func (w *lineWriter) Write(data []byte) (n int, err error) {
	if w.outputMu != nil {
		w.outputMu.Lock()
		defer w.outputMu.Unlock()
	}

	w.m.Lock()
	defer w.m.Unlock()
	if w.closed {
		return 0, io.ErrClosedPipe
	}

	n, err = w.buf.Write(data)
	if err != nil {
		return n, err
	}

	// Look for line breaks. Keep the previous behavior for writers which
	// should not be batched; redirected regular files can combine complete
	// lines into bounded terminal messages.
	buf := w.buf.Bytes()
	if !w.batchStdout {
		i := bytes.LastIndexByte(buf, '\n')
		if i != -1 {
			w.print(string(buf[:i+1]))
			w.buf.Next(i + 1)
		}
		return n, err
	}

	start := 0
	for i, b := range buf {
		if b != '\n' {
			continue
		}
		w.appendLineLocked(buf[start : i+1])
		start = i + 1
	}
	if start > 0 {
		w.buf.Next(start)
	}
	if len(w.batch) > 0 {
		w.scheduleIdleLocked()
	}

	return n, err
}

func (w *lineWriter) Close() error {
	if w.outputMu != nil {
		w.outputMu.Lock()
		defer w.outputMu.Unlock()
	}

	w.m.Lock()
	defer w.m.Unlock()
	return w.closeLocked()
}

func (w *lineWriter) closeLocked() error {
	if w.closed {
		return nil
	}
	w.stopTimerLocked()
	if w.buf.Len() > 0 {
		_ = w.buf.WriteByte('\n')
		w.appendLineLocked(w.buf.Bytes())
		w.buf.Reset()
	}
	w.flushBatchLocked()
	w.closed = true
	return nil
}

// flush sends only complete lines accumulated for a regular-file stdout.
// Callers that share outputMu must hold it first.
func (w *lineWriter) flush() {
	w.m.Lock()
	defer w.m.Unlock()
	w.flushBatchLocked()
}

func (w *lineWriter) appendLineLocked(line []byte) {
	if !w.batchStdout {
		w.print(string(line))
		return
	}

	if len(w.batch) > 0 && len(w.batch)+len(line) > maxOutputBatchSize {
		w.flushBatchLocked()
	}
	if len(line) > maxOutputBatchSize {
		w.flushBatchLocked()
		w.print(string(line))
		return
	}

	w.batch = append(w.batch, line...)
	if len(w.batch) >= maxOutputBatchSize {
		w.flushBatchLocked()
	}
}

func (w *lineWriter) scheduleIdleLocked() {
	if w.idleWindow <= 0 {
		return
	}
	w.lastWrite = time.Now()
	if w.timer == nil {
		w.timer = time.AfterFunc(w.idleWindow, w.flushIdle)
	} else {
		w.timer.Reset(w.idleWindow)
	}
}

func (w *lineWriter) flushIdle() {
	if w.outputMu != nil {
		w.outputMu.Lock()
		defer w.outputMu.Unlock()
	}

	w.m.Lock()
	defer w.m.Unlock()
	if w.closed || len(w.batch) == 0 {
		return
	}
	if idleFor := time.Since(w.lastWrite); idleFor < w.idleWindow {
		w.timer.Reset(w.idleWindow - idleFor)
		return
	}
	w.flushBatchLocked()
}

func (w *lineWriter) flushBatchLocked() {
	if len(w.batch) == 0 {
		return
	}
	w.stopTimerLocked()
	line := string(w.batch)
	w.batch = w.batch[:0]
	w.print(line)
}

func (w *lineWriter) stopTimerLocked() {
	if w.timer != nil {
		w.timer.Stop()
	}
}
