package index

// indexLoadWorkers is the number of workers that load and decode index files.
//
// A worker either downloads an index file or decodes one. Downloads are
// limited by the backend's connections, decoding by the CPUs. Stock runs
// connections+GOMAXPROCS workers, so that all connections stay busy while
// every CPU decodes. Here the decoders beyond the connections are limited to
// two, because each worker also owns the plaintext of an index file until it
// is decoded: connections downloads stay in flight while up to two files are
// decoded, and with many CPUs there is still one worker per CPU.
//
// With one or two CPUs this is stock's count. It is never smaller than
// connections+1, never larger than stock's, and it does not fall with the
// number of CPUs: the downloads follow the connections.
func indexLoadWorkers(connections uint, procs int) uint {
	procs = max(procs, 1)
	return max(connections+uint(min(procs, 2)), uint(procs))
}
