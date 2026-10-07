package termstatus

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	tty "github.com/restic/restic/internal/terminal"
	"github.com/restic/restic/internal/ui"
)

var _ ui.Terminal = &terminal{}

const maxOutputBatchSize = 64 * 1024

type queuedOutput struct {
	err     bool
	line    string
	data    []byte
	offset  int
	started bool
}

// terminal is used to write messages and display status lines which can be
// updated. When the output is redirected to a file, the status lines are not
// printed.
type terminal struct {
	rd               io.ReadCloser
	inFd             uintptr
	wr               io.Writer
	fd               uintptr
	errWriter        io.Writer
	msg              chan message
	status           chan status
	lastStatus       []string
	inputIsTerminal  bool
	outputIsTerminal bool
	canUpdateStatus  bool
	batchStdout      bool
	batchStderr      bool

	outputWriter      io.WriteCloser
	outputWriterOnce  sync.Once
	outputMu          sync.Mutex
	outputBatchWindow time.Duration

	// will be closed when the goroutine which runs Run() terminates, so it'll
	// yield a default value immediately
	closed chan struct{}

	clearCurrentLine func(io.Writer, uintptr) error
	moveCursorUp     func(io.Writer, uintptr, int) error
	moveCursorDown   func(io.Writer, uintptr, int) error
}

type message struct {
	line    string
	err     bool
	barrier chan struct{}
}

type status struct {
	lines []string
}

type fder interface {
	Fd() uintptr
}

// Setup creates a new termstatus. The returned function must be called to shut
// down its background goroutine. It closes OutputWriter and drains submitted
// output before shutdown. If an output writer cannot make progress, the
// remaining bytes cannot be delivered; a short-write error is reported. If a
// record was partially written, its write diagnostics wait until its suffix
// is written or the final shutdown attempt.
//
// A goroutine is started to update the
// terminal. It is terminated when ctx is cancelled. When stdout is redirected to
// a file (e.g. via shell output redirection) or is just an io.Writer (not the
// open *os.File for stdout), no status lines are printed. The status lines and
// normal output (via Print/Printf) are written to stdout, error messages are
// written to stderr. If quiet is set to true, no status messages
// are printed even if the terminal supports it.
//
// Expected usage:
// ```
// term, cancel := termstatus.Setup(os.Stdin, os.Stdout, os.Stderr, false)
// defer cancel()
// // do stuff
// ```
func Setup(stdin io.ReadCloser, stdout, stderr io.Writer, quiet bool) (ui.Terminal, func()) {
	var wg sync.WaitGroup
	// only shutdown once cancel is called to ensure that no output is lost
	cancelCtx, cancel := context.WithCancel(context.Background())

	term := new(stdin, stdout, stderr, quiet)
	wg.Go(func() {
		term.Run(cancelCtx)
	})

	return term, func() {
		term.closeOutputWriter()
		term.Flush()
		// shutdown termstatus
		cancel()
		wg.Wait()
	}
}

func new(rd io.ReadCloser, wr io.Writer, errWriter io.Writer, disableStatus bool) *terminal {
	t := &terminal{
		rd:               rd,
		wr:               wr,
		errWriter:        errWriter,
		msg:              make(chan message),
		status:           make(chan status),
		closed:           make(chan struct{}),
		batchStdout:      isRegularFile(wr),
		batchStderr:      isRegularFile(errWriter),
		outputBatchWindow: 100 * time.Microsecond,
	}

	if disableStatus {
		return t
	}

	if d, ok := rd.(fder); ok {
		if tty.InputIsTerminal(d.Fd()) {
			t.inFd = d.Fd()
			t.inputIsTerminal = true
		}
	}

	if d, ok := wr.(fder); ok {
		if tty.CanUpdateStatus(d.Fd()) {
			// only use the fancy status code when we're running on a real terminal.
			t.canUpdateStatus = true
			t.fd = d.Fd()
			t.clearCurrentLine = tty.ClearCurrentLine(t.fd)
			t.moveCursorUp = tty.MoveCursorUp(t.fd)
			t.moveCursorDown = tty.MoveCursorDown(t.fd)
		}
		if tty.OutputIsTerminal(d.Fd()) {
			t.outputIsTerminal = true
		}
	}

	return t
}

// isRegularFile reports whether w is a regular file. Batching is limited to
// ordinary redirected files so custom writers and pipes retain per-message
// Write behavior.
func isRegularFile(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode().IsRegular()
}

// InputIsTerminal returns whether the input is a terminal.
func (t *terminal) InputIsTerminal() bool {
	return t.inputIsTerminal
}

// InputRaw returns the input reader.
func (t *terminal) InputRaw() io.ReadCloser {
	return t.rd
}

func (t *terminal) ReadPassword(ctx context.Context, prompt string) (string, error) {
	if t.InputIsTerminal() {
		t.Flush()
		return tty.ReadPassword(ctx, int(t.inFd), t.errWriter, prompt)
	}
	if t.OutputIsTerminal() {
		t.Print("reading repository password from stdin")
	}
	return readPassword(t.rd)
}

// readPassword reads the password from the given reader directly.
func readPassword(in io.Reader) (password string, err error) {
	sc := bufio.NewScanner(in)
	sc.Scan()
	if sc.Err() != nil {
		return "", fmt.Errorf("readPassword: %w", sc.Err())
	}
	return sc.Text(), nil
}

// CanUpdateStatus return whether the status output is updated in place.
func (t *terminal) CanUpdateStatus() bool {
	return t.canUpdateStatus
}

// OutputWriter returns a output writer that is safe for concurrent use with
// other output methods. Output is only shown after a line break.
func (t *terminal) OutputWriter() io.Writer {
	t.outputMu.Lock()
	defer t.outputMu.Unlock()

	t.outputWriterOnce.Do(func() {
		t.outputWriter = newTerminalLineWriter(func(line string) {
			t.enqueueOutput(line, false)
		}, &t.outputMu, t.batchStdout, t.outputBatchWindow)
	})
	return t.outputWriter
}

// flushOutputWriterLocked flushes complete lines buffered before the next
// terminal event. The caller must hold outputMu.
func (t *terminal) flushOutputWriterLocked() {
	if w, ok := t.outputWriter.(*lineWriter); ok {
		w.flush()
	}
}

// closeOutputWriter serializes shutdown with concurrent OutputWriter writes.
func (t *terminal) closeOutputWriter() {
	t.outputMu.Lock()
	defer t.outputMu.Unlock()

	if w, ok := t.outputWriter.(*lineWriter); ok {
		w.m.Lock()
		defer w.m.Unlock()
		_ = w.closeLocked()
	}
}

func (t *terminal) enqueueOutput(line string, isErr bool) {
	select {
	case t.msg <- message{line: line, err: isErr}:
	case <-t.closed:
	}
}

// OutputRaw returns the raw output writer. Should only be used if there is no
// other option. Must not be used in combination with Print, Error, SetStatus
// or any other method that writes to the terminal.
func (t *terminal) OutputRaw() io.Writer {
	t.Flush()
	return t.wr
}

// OutputIsTerminal returns whether the output is a terminal.
func (t *terminal) OutputIsTerminal() bool {
	return t.outputIsTerminal
}

// Run updates the screen. It should be run in a separate goroutine. When
// ctx is cancelled, the status lines are cleanly removed.
func (t *terminal) Run(ctx context.Context) {
	defer close(t.closed)
	if t.canUpdateStatus {
		t.run(ctx)
		return
	}

	t.runWithoutStatus(ctx)
}

// run listens on the channels and updates the terminal screen.
func (t *terminal) run(ctx context.Context) {
	var status []string
	for {
		select {
		case <-ctx.Done():
			if !tty.IsProcessBackground(t.fd) {
				t.writeStatus([]string{}, false)
			}

			return

		case msg := <-t.msg:
			if msg.barrier != nil {
				msg.barrier <- struct{}{}
				continue
			}
			if tty.IsProcessBackground(t.fd) {
				// ignore all messages, do nothing, we are in the background process group
				continue
			}
			if err := t.clearCurrentLine(t.wr, t.fd); err != nil {
				t.logWriteErr(err)
				continue
			}

			var dst io.Writer
			if msg.err {
				dst = t.errWriter
			} else {
				dst = t.wr
			}

			if _, err := io.WriteString(dst, msg.line); err != nil {
				t.logWriteErr(err)
				continue
			}

			t.writeStatus(status, false)
		case stat := <-t.status:
			status = append(status[:0], stat.lines...)

			if tty.IsProcessBackground(t.fd) {
				// ignore all messages, do nothing, we are in the background process group
				continue
			}

			t.writeStatus(status, true)
		}
	}
}

func (t *terminal) logWriteErr(err error) {
	if err != nil {
		_, _ = fmt.Fprintf(t.errWriter, "write failed: %v\n", err)
	}
}

func (t *terminal) writeStatus(status []string, skipUnchanged bool) {
	var unchanged []bool
	if skipUnchanged {
		if slices.Equal(status, t.lastStatus) {
			return
		}
		unchanged = findUnchangedLines(status, t.lastStatus)
	}

	lastStatusLen := len(t.lastStatus)
	// Copy the status slice to avoid aliasing
	t.lastStatus = append([]string{}, status...)

	// Extend to clear no longer used status lines
	status = append([]string{}, status...)
	for i := len(status); i < lastStatusLen; i++ {
		status = append(status, "")
	}

	for i, line := range status {
		if unchanged != nil && i < len(unchanged) && unchanged[i] {
			// don't write unchanged lines every frame
			if i < len(status)-1 {
				// just move the cursor down to the next line
				t.logWriteErr(t.moveCursorDown(t.wr, t.fd, 1))
			}
			continue
		}

		t.logWriteErr(t.clearCurrentLine(t.wr, t.fd))

		_, err := t.wr.Write([]byte(line))
		t.logWriteErr(err)
		// all lines except the last one must be followed by a line break
		if i < len(status)-1 {
			_, err := t.wr.Write([]byte("\n"))
			t.logWriteErr(err)
		}
	}

	if len(status) > 0 {
		t.logWriteErr(t.moveCursorUp(t.wr, t.fd, len(status)-1))
	}
}

func findUnchangedLines(curr, last []string) []bool {
	unchanged := make([]bool, len(curr))

	for i := range min(len(curr), len(last)) {
		if curr[i] == last[i] {
			unchanged[i] = true
		}
	}

	return unchanged
}

// runWithoutStatus listens on the channels and just prints out the messages,
// without status lines.
func (t *terminal) runWithoutStatus(ctx context.Context) {
	var lastStatus []string
	var pending []queuedOutput
	var batchBuffer []byte
	var batchTimer *time.Timer
	var batchTimerC <-chan time.Time
	var pendingWriteErrors []error

	stopBatchTimer := func() {
		if batchTimer == nil {
			return
		}
		if !batchTimer.Stop() {
			select {
			case <-batchTimer.C:
			default:
			}
		}
		batchTimer = nil
		batchTimerC = nil
	}
	startBatchTimer := func() {
		if batchTimer == nil {
			batchTimer = time.NewTimer(t.outputBatchWindow)
		} else {
			if !batchTimer.Stop() {
				select {
				case <-batchTimer.C:
				default:
				}
			}
			batchTimer.Reset(t.outputBatchWindow)
		}
		batchTimerC = batchTimer.C
	}
	flushPending := func() bool {
		for len(pending) > 0 {
			item := &pending[0]
			dst := t.wr
			if item.err {
				dst = t.errWriter
			}

			var n int
			var err error
			if item.data != nil {
				remaining := item.data[item.offset:]
				n, err = dst.Write(remaining)
				if n < 0 || n > len(remaining) {
					t.logWriteErr(fmt.Errorf("invalid write count %d", n))
					return false
				}
				item.offset += n
			} else {
				n, err = io.WriteString(dst, item.line)
				if n < 0 || n > len(item.line) {
					t.logWriteErr(fmt.Errorf("invalid write count %d", n))
					return false
				}
				item.line = item.line[n:]
			}

			if n > 0 {
				item.started = true
			}

			if err != nil {
				if item.started {
					// Keep diagnostics behind a partially written record. A later
					// write may recover its suffix, and stderr must not split it.
					pendingWriteErrors = append(pendingWriteErrors, err)
				} else {
					t.logWriteErr(err)
				}
			}
			if item.offset == len(item.data) && item.line == "" {
				pending = pending[1:]
				for _, writeErr := range pendingWriteErrors {
					t.logWriteErr(writeErr)
				}
				pendingWriteErrors = nil
				continue
			}
			if n == 0 {
				if err == nil {
					if item.started {
						pendingWriteErrors = append(pendingWriteErrors, io.ErrShortWrite)
					} else {
						t.logWriteErr(io.ErrShortWrite)
					}
				}
				return false
			}
			// A partial write with an error still made progress. Retry only its
			// unwritten suffix, before allowing later output to reach either stream.
		}
		pending = nil
		if batchBuffer != nil {
			batchBuffer = batchBuffer[:0]
		}
		return true
	}
	flushAndStop := func(reportUndeliveredErrors bool) {
		stopBatchTimer()
		if !flushPending() && reportUndeliveredErrors {
			for _, writeErr := range pendingWriteErrors {
				t.logWriteErr(writeErr)
			}
			pendingWriteErrors = nil
		}
	}
	queueOutput := func(line string, isErr, batchable bool) {
		if batchable && len(line) <= maxOutputBatchSize {
			if len(pending) > 0 {
				last := &pending[len(pending)-1]
				pendingLen := len(last.data) - last.offset
				if last.data == nil || last.err != isErr || last.started || pendingLen+len(line) > maxOutputBatchSize || len(last.data)+len(line) > maxOutputBatchSize {
					stopBatchTimer()
					flushPending()
				}
			}

			if len(pending) > 0 {
				last := &pending[len(pending)-1]
				pendingLen := len(last.data) - last.offset
				if last.data != nil && last.err == isErr && !last.started && pendingLen+len(line) <= maxOutputBatchSize && len(last.data)+len(line) <= maxOutputBatchSize {
					last.data = append(last.data, line...)
					if len(pending) == 1 {
						batchBuffer = last.data
					}
					startBatchTimer()
					return
				}
			}
			var data []byte
			if len(pending) == 0 {
				if batchBuffer == nil {
					batchBuffer = make([]byte, 0, maxOutputBatchSize)
				}
				data = append(batchBuffer[:0], line...)
				batchBuffer = data
			} else {
				data = append(make([]byte, 0, maxOutputBatchSize), line...)
			}
			pending = append(pending, queuedOutput{err: isErr, data: data})
			startBatchTimer()
			return
		}

		stopBatchTimer()
		flushPending()
		// Route even an otherwise empty queue through the retry path so a
		// partial write cannot be overtaken by output for the other stream.
		pending = append(pending, queuedOutput{err: isErr, line: line})
		flushPending()
	}
	for {
		select {
		case <-ctx.Done():
			flushAndStop(true)
			return
		case msg := <-t.msg:
			if msg.barrier != nil {
				flushAndStop(false)
				msg.barrier <- struct{}{}
				continue
			}
			batchable := t.batchStdout
			if msg.err {
				batchable = t.batchStderr
			}
			queueOutput(msg.line, msg.err, batchable)
		case <-batchTimerC:
			batchTimer = nil
			batchTimerC = nil
			flushPending()

		case stat := <-t.status:
			flushAndStop(false)
			if !slices.Equal(stat.lines, lastStatus) {
				for _, line := range stat.lines {
					// Ensure that each message ends with exactly one newline.
					queueOutput(strings.TrimRight(line, "\n")+"\n", false, false)
				}
				// Copy the status slice to avoid aliasing
				lastStatus = append([]string{}, stat.lines...)
			}
		}
	}
}

// Flush waits until messages queued before the barrier have been written or an
// output writer stops making progress. A short write before any bytes are
// written is reported immediately. If a record was partially written, its
// diagnostics wait until its suffix is written or the final shutdown attempt.
// Remaining bytes stay queued for later output or the final shutdown attempt.
func (t *terminal) Flush() {
	t.outputMu.Lock()
	defer t.outputMu.Unlock()
	t.flushOutputWriterLocked()

	ch := make(chan struct{})
	defer close(ch)
	select {
	case t.msg <- message{barrier: ch}:
	case <-t.closed:
	}
	select {
	case <-ch:
	case <-t.closed:
	}
}

func (t *terminal) print(line string, isErr bool) {
	t.outputMu.Lock()
	defer t.outputMu.Unlock()
	t.flushOutputWriterLocked()

	// make sure the line ends with a line break
	if len(line) == 0 || line[len(line)-1] != '\n' {
		line += "\n"
	}

	t.enqueueOutput(line, isErr)
}

// Print writes a line to the terminal.
func (t *terminal) Print(line string) {
	t.print(line, false)
}

// Error writes an error to the terminal.
func (t *terminal) Error(line string) {
	t.print(line, true)
}

func sanitizeLines(lines []string, width int) []string {
	sanitized := make([]string, len(lines))
	// Sanitize lines and truncate them if they're too long.
	for i, line := range lines {
		line = ui.Quote(line)
		if width > 0 {
			line = ui.Truncate(line, width-2)
		}
		sanitized[i] = line
	}
	return sanitized
}

// SetStatus updates the status lines.
// The lines should not contain newlines; this method adds them.
// Pass nil or an empty array to remove the status lines.
func (t *terminal) SetStatus(lines []string) {
	// only truncate interactive status output
	var width int
	if t.canUpdateStatus {
		width = tty.Width(t.fd)
		if width <= 0 {
			// use 80 columns by default
			width = 80
		}
	}

	lines = sanitizeLines(lines, width)
	t.outputMu.Lock()
	defer t.outputMu.Unlock()
	t.flushOutputWriterLocked()

	select {
	case t.status <- status{lines: lines}:
	case <-t.closed:
	}
}
