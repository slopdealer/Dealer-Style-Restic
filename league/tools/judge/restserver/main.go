// restserver: the judge-side REST backend of the "remote" workload (restic-v2/judge/REMOTE.md).
//
// A minimal restic REST server (protocol v2 list format, v1 fallback) over a unix socket, standard library only,
// compiled once with the judge's frozen toolchain and frozen into workloads-remote/bin/. It stores files exactly where
// restic's local backend stores them (config, data/<2 hex>/<name>, index/, keys/, locks/, snapshots/; files 0400,
// directories 0700), so a repository written through it is read by stock restic as a local repository (the oracle).
//
// The network is simulated INSIDE the server, deterministically, with sleeps only (never a busy wait):
//   -latency-ms       fixed first-byte delay of every request (slept before the request is looked at)
//   -rate             bytes per second per connection for bodies in both directions (paced against a per-request
//                     schedule: byte n is not delivered before start + n/rate, so sleep overshoot never accumulates)
//   -max-connections  requests in flight at once; further requests queue (FIFO) before their latency starts
// On SIGTERM/SIGINT it writes the request statistics (-stats) and exits. One server serves one restic process.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	Requests int64   `json:"requests"`
	BytesIn  int64   `json:"bytes_in"`
	BytesOut int64   `json:"bytes_out"`
	Seconds  float64 `json:"seconds"`
	NotOK    int64   `json:"not_ok"`
	Partial  int64   `json:"partial_gets"`
}

type server struct {
	root    string
	latency time.Duration
	rate    float64
	slots   chan struct{}

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

// pacer delays so that cumulative byte n is delivered no earlier than start + n/rate.
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
	status int
	out    int64
}

func (w *pacedWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *pacedWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	done := 0
	for done < len(b) {
		end := done + chunk
		if end > len(b) {
			end = len(b)
		}
		w.p.add(end - done) // the chunk "arrives" when its last byte would
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

func (s *server) handle(rw http.ResponseWriter, r *http.Request) {
	arrived := time.Now()
	s.slots <- struct{}{} // a connection slot; FIFO queue beyond -max-connections
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
	s.mu.Unlock()

	time.Sleep(s.latency) // round trip before the first byte
	p := &pacer{rate: s.rate, start: time.Now()}
	w := &pacedWriter{ResponseWriter: rw, p: p}
	kind, in, partial := s.serve(w, r, p)
	if w.status == 0 {
		w.status = http.StatusOK
	}

	ended := time.Now()
	s.mu.Lock()
	key := r.Method + " " + kind
	c := s.by[key]
	if c == nil {
		c = &counter{}
		s.by[key] = c
	}
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
	s.inFlight--
	s.last = ended
	if s.logFile != nil {
		fmt.Fprintf(s.logFile, "{\"t_ms\":%.3f,\"method\":%q,\"path\":%q,\"range\":%q,\"status\":%d,\"in\":%d,\"out\":%d,\"ms\":%.3f}\n",
			arrived.Sub(s.started).Seconds()*1000, r.Method, r.URL.Path, r.Header.Get("Range"), w.status, in, w.out,
			ended.Sub(arrived).Seconds()*1000)
	}
	s.mu.Unlock()
	<-s.slots
}

// serve returns the file type (for the statistics), the request body bytes read and whether a GET served only a part.
func (s *server) serve(w *pacedWriter, r *http.Request, p *pacer) (string, int64, bool) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	isDir := strings.HasSuffix(r.URL.Path, "/")
	switch {
	case len(parts) == 1 && parts[0] == "": // repository root
		if r.Method == http.MethodPost && r.URL.Query().Get("create") == "true" {
			return "create", 0, s.create(w)
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return "root", 0, false
	case len(parts) == 1 && parts[0] == "config" && !isDir:
		in, partial := s.file(w, r, p, "config", "")
		return "config", in, partial
	case len(parts) == 1 && fileTypes[parts[0]]:
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return parts[0] + "/", 0, false
		}
		s.list(w, r, parts[0])
		return parts[0] + "/", 0, false
	case len(parts) == 2 && fileTypes[parts[0]] && validName(parts[1]) && !isDir:
		in, partial := s.file(w, r, p, parts[0], parts[1])
		return parts[0], in, partial
	}
	http.Error(w, "not found", http.StatusNotFound)
	return "invalid", 0, false
}

func (s *server) create(w http.ResponseWriter) bool {
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
			return false
		}
	}
	w.WriteHeader(http.StatusOK)
	return false
}

func (s *server) list(w http.ResponseWriter, r *http.Request, kind string) {
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
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *server) file(w *pacedWriter, r *http.Request, p *pacer, kind, name string) (int64, bool) {
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
		http.ServeContent(w, r, "", time.Time{}, f) // Range, 206/416 and Content-Length handled by net/http
		return 0, w.status == http.StatusPartialContent && w.out < info.Size()
	case http.MethodPost:
		if _, err := os.Lstat(path); err == nil {
			_, _ = io.Copy(io.Discard, r.Body)
			http.Error(w, "file exists", http.StatusForbidden) // as rest-server: no overwrite
			return 0, false
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return 0, false
		}
		tmp := path + ".rest-tmp"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return 0, false
		}
		buf := make([]byte, chunk)
		var in int64
		var failure error
		for {
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
		if failure == nil && r.ContentLength >= 0 && in != r.ContentLength {
			failure = fmt.Errorf("short body: %d of %d", in, r.ContentLength)
		}
		if failure == nil {
			failure = os.Chmod(tmp, 0o400) // restic's local backend leaves files read-only
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
	var requests, in, out, notOK int64
	var busy float64
	for _, c := range s.by {
		requests += c.Requests
		in += c.BytesIn
		out += c.BytesOut
		notOK += c.NotOK
		busy += c.Seconds
	}
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	cpu := float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6
	span := 0.0
	if !s.first.IsZero() {
		span = s.last.Sub(s.first).Seconds()
	}
	return map[string]interface{}{
		"schema": "restic-league-restserver-stats-v1", "profile": profile,
		"requests": requests, "bytes_in": in, "bytes_out": out, "not_ok": notOK,
		"latency_seconds": float64(requests) * s.latency.Seconds(), "request_seconds": busy,
		"span_seconds": span, "max_in_flight": s.maxInFlight, "queued_requests": s.queued,
		"queue_seconds": s.queueSecs, "server_cpu_seconds": cpu, "by": s.by,
	}
}

func main() {
	listen := flag.String("listen", "", "unix socket path")
	root := flag.String("root", "", "repository directory (local repository layout)")
	latencyMs := flag.Float64("latency-ms", 30, "first-byte delay per request, milliseconds")
	rate := flag.Float64("rate", 12*1024*1024, "bytes per second per connection, both directions (0 = unlimited)")
	maxConn := flag.Int("max-connections", 5, "requests in flight at once; more queue")
	statsPath := flag.String("stats", "", "write request statistics here at exit")
	logPath := flag.String("log", "", "optional per-request JSON lines log")
	flag.Parse()
	if *listen == "" || *root == "" || *maxConn < 1 {
		fmt.Fprintln(os.Stderr, "restserver: -listen and -root are required")
		os.Exit(2)
	}
	s := &server{root: *root, latency: time.Duration(*latencyMs * float64(time.Millisecond)), rate: *rate,
		slots: make(chan struct{}, *maxConn), by: map[string]*counter{}, started: time.Now()}
	if *logPath != "" {
		f, err := os.Create(*logPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "restserver:", err)
			os.Exit(1)
		}
		s.logFile = f
	}
	_ = os.Remove(*listen)
	listener, err := net.Listen("unix", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "restserver:", err)
		os.Exit(1)
	}
	profile := map[string]interface{}{"latency_ms": *latencyMs, "rate_bytes_per_second": *rate, "max_connections": *maxConn}
	srv := &http.Server{Handler: http.HandlerFunc(s.handle)}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	go func() { _ = srv.Serve(listener) }()
	<-signals
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
