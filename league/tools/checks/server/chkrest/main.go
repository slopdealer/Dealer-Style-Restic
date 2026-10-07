// chkrest: REST backend for the restic release checks (final-candidate/checks).
//
// Derived from the league's judge server (restic-v2/judge/restserver/main.go): a minimal restic REST server over a
// unix socket, standard library only, that stores files exactly where restic's local backend stores them, so the same
// directory can be opened as a local repository by another process at the same time.
//
// Added for the checks (all inside this process; no host networking is touched):
//
//	-hold-lock-post-ms N   hold every POST /locks/<id> for N ms BEFORE the file is written (the lock is not visible
//	                       to anyone until the hold is over): the "slow lock upload" of repair R1
//	-hold-lock-after       hold AFTER the file is written instead (visible early, answer late: a lost response)
//	-fail-after K          the K-th matching request (1-based) and the next -fail-count-1 matching ones are failed
//	-fail-count M
//	-fail-mode 500|drop|half|stall   500: answer 500 without doing anything; drop: close the connection without an
//	                       answer; half: process half of the body (upload) or send part of the answer (download), then
//	                       close; stall: wait -stall-ms, then serve the request normally
//	-fail-method, -fail-kind   which requests count as matching (default: every request)
//	-damage-file PATH -damage-trigger "POST data"   when the first request with that method and kind arrives, one
//	                       byte in the middle of PATH is flipped on disk, once (the file keeps its size): a file that
//	                       changes between two reads of the same client
//	SIGUSR1 / SIGUSR2      outage on / off: while on, every request (also those in flight) loses its connection
//	-latency-ms, -rate, -max-connections   as in the judge server
//	-stats FILE            request statistics at exit, with the largest number of requests in flight per file type
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	contentTypeV1 = "application/vnd.x.restic.rest.v1"
	contentTypeV2 = "application/vnd.x.restic.rest.v2"
	chunk         = 64 * 1024
)

var fileTypes = map[string]bool{"data": true, "keys": true, "locks": true, "snapshots": true, "index": true}

type counter struct {
	Requests    int64   `json:"requests"`
	BytesIn     int64   `json:"bytes_in"`
	BytesOut    int64   `json:"bytes_out"`
	Seconds     float64 `json:"seconds"`
	NotOK       int64   `json:"not_ok"`
	Partial     int64   `json:"partial_gets"`
	Aborted     int64   `json:"aborted"`
	InFlight    int     `json:"-"`
	MaxInFlight int     `json:"max_in_flight"`
}

type server struct {
	root      string
	latency   time.Duration
	rate      float64
	slots     chan struct{}
	holdLock  time.Duration
	holdAfter bool
	stall     time.Duration

	damageFile, damageTrigger string
	damaged                   int32

	failAfter, failCount int64
	failMode, failMethod string
	failKind             string
	matched              int64
	injected             int64
	outage               int32

	mu          sync.Mutex
	by          map[string]*counter
	inFlight    int
	maxInFlight int
	queued      int64
	queueSecs   float64
	first, last time.Time
	logFile     *os.File
	started     time.Time
}

type pacer struct {
	rate  float64
	start time.Time
	n     int64
}

func (p *pacer) add(n int) {
	if p.rate <= 0 || n <= 0 {
		return
	}
	p.n += int64(n)
	target := p.start.Add(time.Duration(float64(p.n) / p.rate * float64(time.Second)))
	if d := time.Until(target); d > 0 {
		time.Sleep(d)
	}
}

type pacedWriter struct {
	http.ResponseWriter
	p      *pacer
	s      *server
	status int
	out    int64
	cutAt  int64 // > 0: abort the connection once this many body bytes were sent
}

func (w *pacedWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *pacedWriter) abort() {
	panic(http.ErrAbortHandler) // net/http closes the connection without finishing the answer
}

func (w *pacedWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	done := 0
	for done < len(b) {
		if atomic.LoadInt32(&w.s.outage) != 0 {
			w.abort()
		}
		end := done + chunk
		if end > len(b) {
			end = len(b)
		}
		if w.cutAt > 0 && w.out+int64(end-done) >= w.cutAt {
			end = done + int(w.cutAt-w.out)
			if end > done {
				n, _ := w.ResponseWriter.Write(b[done:end])
				w.out += int64(n)
			}
			if f, ok := w.ResponseWriter.(http.Flusher); ok {
				f.Flush()
			}
			w.abort()
		}
		w.p.add(end - done)
		n, err := w.ResponseWriter.Write(b[done:end])
		done += n
		w.out += int64(n)
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

func (s *server) path(kind, name string) string {
	if kind == "config" {
		return filepath.Join(s.root, "config")
	}
	if kind == "data" && len(name) > 2 {
		return filepath.Join(s.root, "data", name[:2], name)
	}
	return filepath.Join(s.root, kind, name)
}

func validName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, c := range name {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func kindOf(r *http.Request) string {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	isDir := strings.HasSuffix(r.URL.Path, "/")
	switch {
	case len(parts) == 1 && parts[0] == "":
		return "root"
	case len(parts) == 1 && parts[0] == "config" && !isDir:
		return "config"
	case len(parts) == 1 && fileTypes[parts[0]]:
		return parts[0] + "/"
	case len(parts) == 2 && fileTypes[parts[0]] && validName(parts[1]) && !isDir:
		return parts[0]
	}
	return "invalid"
}

// fault decides whether this request is one of the injected failures.
func (s *server) fault(r *http.Request, kind string) bool {
	if s.failAfter <= 0 || s.failCount <= 0 {
		return false
	}
	if s.failMethod != "" && r.Method != s.failMethod {
		return false
	}
	if s.failKind != "" && kind != s.failKind {
		return false
	}
	n := atomic.AddInt64(&s.matched, 1)
	if n >= s.failAfter && n < s.failAfter+s.failCount {
		atomic.AddInt64(&s.injected, 1)
		return true
	}
	return false
}

// damage flips one byte in the middle of the configured file.
func (s *server) damage() {
	info, err := os.Stat(s.damageFile)
	if err != nil || info.Size() == 0 {
		fmt.Fprintln(os.Stderr, "chkrest: damage:", err)
		return
	}
	_ = os.Chmod(s.damageFile, 0o600)
	f, err := os.OpenFile(s.damageFile, os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "chkrest: damage:", err)
		return
	}
	defer f.Close()
	one := make([]byte, 1)
	at := info.Size() / 2
	if _, err := f.ReadAt(one, at); err == nil {
		one[0] ^= 0x01
		_, _ = f.WriteAt(one, at)
	}
	_ = os.Chmod(s.damageFile, 0o400)
}

func (s *server) handle(rw http.ResponseWriter, r *http.Request) {
	arrived := time.Now()
	kind := kindOf(r)
	if s.damageFile != "" && r.Method+" "+kind == s.damageTrigger && atomic.CompareAndSwapInt32(&s.damaged, 0, 1) {
		s.damage()
	}
	s.slots <- struct{}{}
	begun := time.Now()
	s.mu.Lock()
	if s.first.IsZero() {
		s.first = arrived
	}
	if waited := begun.Sub(arrived); waited > time.Millisecond {
		s.queued++
		s.queueSecs += waited.Seconds()
	}
	s.inFlight++
	if s.inFlight > s.maxInFlight {
		s.maxInFlight = s.inFlight
	}
	key := r.Method + " " + kind
	c := s.by[key]
	if c == nil {
		c = &counter{}
		s.by[key] = c
	}
	c.InFlight++
	if c.InFlight > c.MaxInFlight {
		c.MaxInFlight = c.InFlight
	}
	s.mu.Unlock()

	p := &pacer{rate: s.rate}
	w := &pacedWriter{ResponseWriter: rw, p: p, s: s}
	var in int64
	var partial bool
	defer func() {
		rec := recover()
		ended := time.Now()
		s.mu.Lock()
		c.Requests++
		c.BytesIn += in
		c.BytesOut += w.out
		c.Seconds += ended.Sub(arrived).Seconds()
		if w.status >= 300 {
			c.NotOK++
		}
		if partial {
			c.Partial++
		}
		if rec != nil {
			c.Aborted++
		}
		c.InFlight--
		s.inFlight--
		s.last = ended
		if s.logFile != nil {
			fmt.Fprintf(s.logFile, "{\"t_ms\":%.3f,\"abs_us\":%d,\"method\":%q,\"path\":%q,\"range\":%q,\"status\":%d,\"in\":%d,\"out\":%d,\"ms\":%.3f,\"aborted\":%v}\n",
				arrived.Sub(s.started).Seconds()*1000, arrived.UnixMicro(), r.Method, r.URL.Path, r.Header.Get("Range"), w.status, in, w.out,
				ended.Sub(arrived).Seconds()*1000, rec != nil)
		}
		s.mu.Unlock()
		<-s.slots
		if rec != nil {
			panic(rec) // http.ErrAbortHandler: let net/http drop the connection
		}
	}()

	time.Sleep(s.latency)
	if atomic.LoadInt32(&s.outage) != 0 {
		w.abort()
	}
	half := false
	if s.fault(r, kind) {
		switch s.failMode {
		case "500":
			_, _ = io.Copy(io.Discard, r.Body)
			http.Error(w, "injected failure", http.StatusInternalServerError)
			return
		case "drop":
			w.abort()
		case "stall":
			time.Sleep(s.stall)
		default:
			half = true
		}
	}
	isLockPost := r.Method == http.MethodPost && kind == "locks"
	if isLockPost && s.holdLock > 0 && !s.holdAfter {
		time.Sleep(s.holdLock)
	}
	p.start = time.Now()
	in, partial = s.serve(w, r, p, kind, half)
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if isLockPost && s.holdLock > 0 && s.holdAfter {
		time.Sleep(s.holdLock)
	}
}

func (s *server) serve(w *pacedWriter, r *http.Request, p *pacer, kind string, half bool) (int64, bool) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case kind == "root":
		if r.Method == http.MethodPost && r.URL.Query().Get("create") == "true" {
			s.create(w)
			return 0, false
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return 0, false
	case kind == "config":
		return s.file(w, r, p, "config", "", half)
	case strings.HasSuffix(kind, "/"):
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return 0, false
		}
		s.list(w, r, parts[0], half)
		return 0, false
	case kind != "invalid":
		return s.file(w, r, p, parts[0], parts[1], half)
	}
	http.Error(w, "not found", http.StatusNotFound)
	return 0, false
}

func (s *server) create(w http.ResponseWriter) {
	dirs := []string{s.root, filepath.Join(s.root, "data")}
	for i := 0; i < 256; i++ {
		dirs = append(dirs, filepath.Join(s.root, "data", fmt.Sprintf("%02x", i)))
	}
	for _, name := range []string{"index", "keys", "locks", "snapshots"} {
		dirs = append(dirs, filepath.Join(s.root, name))
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *server) list(w *pacedWriter, r *http.Request, kind string, half bool) {
	type item struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	items := []item{}
	dirs := []string{filepath.Join(s.root, kind)}
	if kind == "data" {
		subs, err := os.ReadDir(dirs[0])
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		dirs = dirs[:0]
		for _, sub := range subs {
			if sub.IsDir() {
				dirs = append(dirs, filepath.Join(s.root, kind, sub.Name()))
			}
		}
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) && kind != "data" {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			continue
		}
		for _, entry := range entries {
			if !entry.Type().IsRegular() || !validName(entry.Name()) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			items = append(items, item{entry.Name(), info.Size()})
		}
	}
	sort.Slice(items, func(a, b int) bool { return items[a].Name < items[b].Name })
	var body []byte
	if strings.Contains(r.Header.Get("Accept"), contentTypeV2) {
		w.Header().Set("Content-Type", contentTypeV2)
		body, _ = json.Marshal(items)
	} else {
		w.Header().Set("Content-Type", contentTypeV1)
		names := make([]string, len(items))
		for i, it := range items {
			names[i] = it.Name
		}
		body, _ = json.Marshal(names)
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	if half {
		w.cutAt = int64(len(body)/2) + 1
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *server) file(w *pacedWriter, r *http.Request, p *pacer, kind, name string, half bool) (int64, bool) {
	path := s.path(kind, name)
	switch r.Method {
	case http.MethodHead:
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			http.Error(w, "not found", http.StatusNotFound)
			return 0, false
		}
		w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		f, err := os.Open(path)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return 0, false
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.Error(w, "not found", http.StatusNotFound)
			return 0, false
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if half {
			w.cutAt = 1 + info.Size()/64 // early enough to fall inside any ranged answer
		}
		http.ServeContent(w, r, "", time.Time{}, f)
		return 0, w.status == http.StatusPartialContent && w.out < info.Size()
	case http.MethodPost:
		if _, err := os.Lstat(path); err == nil {
			_, _ = io.Copy(io.Discard, r.Body)
			http.Error(w, "file exists", http.StatusForbidden)
			return 0, false
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return 0, false
		}
		tmp := fmt.Sprintf("%s.rest-tmp-%d", path, time.Now().UnixNano())
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return 0, false
		}
		buf := make([]byte, chunk)
		var in int64
		var failure error
		cut := int64(-1)
		if half && r.ContentLength > 0 {
			cut = r.ContentLength / 2
		} else if half {
			cut = 0
		}
		dropped := false
		for {
			if atomic.LoadInt32(&s.outage) != 0 || (cut >= 0 && in >= cut) {
				dropped = true
				break
			}
			n, rerr := r.Body.Read(buf)
			if n > 0 {
				p.add(n)
				in += int64(n)
				if _, werr := f.Write(buf[:n]); werr != nil {
					failure = werr
					break
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				failure = rerr
				break
			}
		}
		if cerr := f.Close(); failure == nil {
			failure = cerr
		}
		if dropped {
			_ = os.Remove(tmp)
			w.abort()
		}
		if failure == nil && r.ContentLength >= 0 && in != r.ContentLength {
			failure = fmt.Errorf("short body: %d of %d", in, r.ContentLength)
		}
		if failure == nil {
			failure = os.Chmod(tmp, 0o400)
		}
		if failure == nil {
			failure = os.Rename(tmp, path)
		}
		if failure != nil {
			_ = os.Remove(tmp)
			http.Error(w, failure.Error(), http.StatusInternalServerError)
			return in, false
		}
		w.WriteHeader(http.StatusOK)
		return in, false
	case http.MethodDelete:
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "not found", http.StatusNotFound)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return 0, false
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
	return 0, false
}

func (s *server) stats(profile map[string]interface{}) map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	var requests, in, out, notOK, aborted int64
	var busy float64
	for _, c := range s.by {
		requests += c.Requests
		in += c.BytesIn
		out += c.BytesOut
		notOK += c.NotOK
		aborted += c.Aborted
		busy += c.Seconds
	}
	span := 0.0
	if !s.first.IsZero() {
		span = s.last.Sub(s.first).Seconds()
	}
	return map[string]interface{}{
		"schema": "restic-checks-chkrest-stats-v1", "profile": profile,
		"requests": requests, "bytes_in": in, "bytes_out": out, "not_ok": notOK, "aborted": aborted,
		"injected_failures": atomic.LoadInt64(&s.injected), "request_seconds": busy, "damage_done": atomic.LoadInt32(&s.damaged) != 0,
		"span_seconds": span, "max_in_flight": s.maxInFlight, "queued_requests": s.queued,
		"queue_seconds": s.queueSecs, "by": s.by,
	}
}

func main() {
	listen := flag.String("listen", "", "unix socket path")
	root := flag.String("root", "", "repository directory (local repository layout)")
	latencyMs := flag.Float64("latency-ms", 0, "first-byte delay per request, milliseconds")
	rate := flag.Float64("rate", 0, "bytes per second per connection, both directions (0 = unlimited)")
	maxConn := flag.Int("max-connections", 64, "requests in flight at once; more queue")
	holdLock := flag.Float64("hold-lock-post-ms", 0, "hold every POST /locks/<id> this long")
	holdAfter := flag.Bool("hold-lock-after", false, "hold after the lock file is written instead of before")
	failAfter := flag.Int64("fail-after", 0, "fail the K-th matching request (1-based); 0 = never")
	failCount := flag.Int64("fail-count", 1, "how many matching requests in a row are failed")
	failMode := flag.String("fail-mode", "500", "500 | drop | half | stall")
	stallMs := flag.Float64("stall-ms", 5000, "how long a stalled request waits before it is served")
	failMethod := flag.String("fail-method", "", "only requests with this method match (empty: all)")
	failKind := flag.String("fail-kind", "", "only requests of this kind match: data, index, snapshots, locks, keys, config, data/ ... (empty: all)")
	damageFile := flag.String("damage-file", "", "flip one byte in the middle of this file when the trigger request arrives")
	damageTrigger := flag.String("damage-trigger", "POST data", "method and kind of the request that triggers the damage")
	statsPath := flag.String("stats", "", "write request statistics here at exit")
	logPath := flag.String("log", "", "optional per-request JSON lines log")
	flag.Parse()
	if *listen == "" || *root == "" || *maxConn < 1 {
		fmt.Fprintln(os.Stderr, "chkrest: -listen and -root are required")
		os.Exit(2)
	}
	s := &server{root: *root, latency: time.Duration(*latencyMs * float64(time.Millisecond)), rate: *rate,
		slots: make(chan struct{}, *maxConn), by: map[string]*counter{}, started: time.Now(),
		holdLock: time.Duration(*holdLock * float64(time.Millisecond)), holdAfter: *holdAfter,
		stall: time.Duration(*stallMs * float64(time.Millisecond)), damageFile: *damageFile, damageTrigger: *damageTrigger,
		failAfter: *failAfter, failCount: *failCount, failMode: *failMode, failMethod: *failMethod, failKind: *failKind}
	if *logPath != "" {
		f, err := os.Create(*logPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "chkrest:", err)
			os.Exit(1)
		}
		s.logFile = f
	}
	_ = os.Remove(*listen)
	listener, err := net.Listen("unix", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "chkrest:", err)
		os.Exit(1)
	}
	profile := map[string]interface{}{"latency_ms": *latencyMs, "rate_bytes_per_second": *rate, "max_connections": *maxConn,
		"hold_lock_post_ms": *holdLock, "hold_lock_after": *holdAfter, "fail_after": *failAfter, "fail_count": *failCount,
		"fail_mode": *failMode, "fail_method": *failMethod, "fail_kind": *failKind}
	srv := &http.Server{Handler: http.HandlerFunc(s.handle), ErrorLog: log.New(io.Discard, "", 0)}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1, syscall.SIGUSR2)
	go func() { _ = srv.Serve(listener) }()
	for sig := range signals {
		if sig == syscall.SIGUSR1 {
			atomic.StoreInt32(&s.outage, 1)
			continue
		}
		if sig == syscall.SIGUSR2 {
			atomic.StoreInt32(&s.outage, 0)
			continue
		}
		break
	}
	_ = srv.Close()
	if s.logFile != nil {
		_ = s.logFile.Close()
	}
	if *statsPath != "" {
		data, _ := json.MarshalIndent(s.stats(profile), "", " ")
		tmp := *statsPath + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err == nil {
			_ = os.Rename(tmp, *statsPath)
		}
	}
	_ = os.Remove(*listen)
}
