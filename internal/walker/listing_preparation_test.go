package walker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

func TestListingPreparationOverlapAndJoin(t *testing.T) {
	for _, earlyStop := range []bool{false, true} {
		p := newListingPreparation(nil)
		ready := make(chan struct{})
		var closed atomic.Bool
		var cursor int
		parseErr := errors.New("prepared parse error")
		source := listingSource{
			size:  func(int) (int, bool) { return 10, true },
			close: func() { closed.Store(true) },
			next: func() (data.NodeOrError, bool) {
				if cursor == listingBatchNodes {
					close(ready)
				}
				if cursor == listingBatchNodes*2 {
					cursor++
					return data.NodeOrError{Error: parseErr}, true
				}
				if cursor > listingBatchNodes*2 {
					return data.NodeOrError{}, false
				}
				n := &data.Node{Name: "file", Type: data.NodeTypeFile, Inode: uint64(cursor)}
				cursor++
				return data.NodeOrError{Node: n}, true
			},
		}
		count := 0
		for item := range p.wrap(context.Background(), source) {
			if count == 0 {
				select {
				case <-ready:
				case <-time.After(5 * time.Second):
					t.Fatal("no decoding while callback is occupied")
				}
				if earlyStop {
					break
				}
			}
			if item.Error != nil {
				if item.Error != parseErr || count != 2*listingBatchNodes {
					t.Fatalf("error at wrong position: %d, %v", count, item.Error)
				}
				break
			}
			if item.Node.Inode != uint64(count) {
				t.Fatal("node order changed")
			}
			count++
		}
		if !closed.Load() {
			t.Fatal("reader not closed after pending decoder joined")
		}
		p.Close()
		p.Close()
	}
}

func TestListingPreparationBoundaryAndOversized(t *testing.T) {
	p := newListingPreparation(nil)
	defer p.Close()
	for _, boundary := range []bool{false, true} {
		var cursor int
		var ahead atomic.Bool
		source := listingSource{
			close: func() {},
			size:  func(int) (int, bool) { return 10, boundary || cursor != 0 },
			next: func() (data.NodeOrError, bool) {
				cursor++
				if cursor == 1 {
					typeOfNode := data.NodeTypeFile
					if boundary {
						typeOfNode = data.NodeTypeDir
					}
					return data.NodeOrError{Node: &data.Node{Name: "first", Type: typeOfNode}}, true
				}
				ahead.Store(true)
				return data.NodeOrError{}, false
			},
		}
		for range p.wrap(context.Background(), source) {
			if ahead.Load() {
				t.Fatal("decoder crossed directory/oversized boundary")
			}
		}
	}
}

type listingTestLoader struct {
	payload []byte
	err     error
	loads   int
}

func (l *listingTestLoader) LoadBlob(_ context.Context, _ restic.BlobHandle, _ []byte) ([]byte, error) {
	l.loads++
	if l.err != nil {
		return nil, l.err
	}
	return append([]byte(nil), l.payload...), nil
}

func listingEvents(ctx context.Context, load *listingTestLoader, prepared bool, stop string, callbackErr error, root func()) ([]string, error) {
	events := []string{}
	visitor := WalkVisitor{
		ProcessNode: func(_ restic.ID, path string, node *data.Node, err error) error {
			events = append(events, fmt.Sprintf("%s:%v", path, err))
			if path == "/" && root != nil {
				root()
			}
			if err != nil {
				return err
			}
			if path == stop {
				return callbackErr
			}
			return nil
		},
		LeaveDir: func(path string) error { events = append(events, "leave:"+path); return nil },
	}
	var err error
	if prepared {
		err = WalkPrepared(ctx, load, restic.ID{}, visitor)
	} else {
		err = Walk(ctx, load, restic.ID{}, visitor)
	}
	return events, err
}

func TestListingPreparationMatchesOrdinaryErrorsAndObservation(t *testing.T) {
	callbackErr := errors.New("callback failed")
	for _, payload := range []string{
		`{"nodes":[]}`,
		`{"nodes":[{"name":"a","type":"file"},{"name":"b","type":"file"}]}`,
		`{"nodes":[{"name":"a","type":"file"},{"name":1}]}`,
		`{"nodes":[{"name":"a","type":"file"},{"name":`,
		`{"nodes":[{"name":"a","type":"file"},{"name":"b","type":"dir"}]}`,
		`{"nodes":[{"name":"a","type":"file","user":"` + strings.Repeat("x", listingNodeJSONBytes) + `"},{"name":"b","type":"file"}]}`,
		`{"nodes":[],"tail":`,
	} {
		for _, stopErr := range []error{nil, callbackErr, ErrSkipNode} {
			for _, canceled := range []bool{false, true} {
				var ordinary []string
				var ordinaryErr error
				for _, prepared := range []bool{false, true} {
					ctx, cancel := context.WithCancel(context.Background())
					load := &listingTestLoader{payload: []byte(payload)}
					root := func() {
						// Post-observation replacement/removal cannot invalidate
						// the acquired bytes, and must not trigger a second load.
						load.payload = []byte(`{"nodes":[]}`)
						load.err = errors.New("external disappearance")
						if canceled {
							cancel()
						}
					}
					events, err := listingEvents(ctx, load, prepared, "/a", stopErr, root)
					cancel()
					if load.loads != 1 {
						t.Fatalf("extra observation: %d", load.loads)
					}
					if !prepared {
						ordinary, ordinaryErr = events, err
						continue
					}
					if !reflect.DeepEqual(events, ordinary) || fmt.Sprintf("%T:%v", err, err) != fmt.Sprintf("%T:%v", ordinaryErr, ordinaryErr) {
						t.Fatalf("observable difference: %v / %v; %v / %v", ordinary, events, ordinaryErr, err)
					}
				}
			}
		}
	}
	// Disappearance before the ordinary read remains an ordinary load failure.
	for _, prepared := range []bool{false, true} {
		missing := errors.New("missing before load")
		load := &listingTestLoader{err: missing}
		_, err := listingEvents(context.Background(), load, prepared, "", nil, nil)
		if err != missing || load.loads != 1 {
			t.Fatal("load failure was substituted")
		}
	}
}

func TestListingPreparationNestedWalkMatchesOrdinary(t *testing.T) {
	trees, root := BuildTreeMap(TestTree{"a": TestFile{}, "b": TestTree{"a": TestFile{}, "b": TestTree{"c": TestFile{}}}, "z": TestFile{}})
	var want []string
	for _, prepared := range []bool{false, true} {
		var got []string
		visitor := WalkVisitor{ProcessNode: func(_ restic.ID, p string, _ *data.Node, err error) error { got = append(got, p); return err }, LeaveDir: func(p string) error { got = append(got, "leave:"+p); return nil }}
		var err error
		if prepared {
			err = WalkPrepared(context.Background(), trees, root, visitor)
		} else {
			err = Walk(context.Background(), trees, root, visitor)
		}
		if err != nil {
			t.Fatal(err)
		}
		if !prepared {
			want = got
		} else if !reflect.DeepEqual(got, want) {
			t.Fatalf("%v / %v", got, want)
		}
	}
}

func TestListingPreparationPanicJoins(t *testing.T) {
	p := newListingPreparation(nil)
	var closed atomic.Bool
	source := listingSource{size: func(int) (int, bool) { return 10, true }, next: func() (data.NodeOrError, bool) {
		return data.NodeOrError{Node: &data.Node{Type: data.NodeTypeFile}}, true
	}, close: func() { closed.Store(true) }}
	func() {
		defer func() {
			if recover() != "visitor" {
				t.Fatal("panic changed")
			}
		}()
		for range p.wrap(context.Background(), source) {
			panic("visitor")
		}
	}()
	if !closed.Load() {
		t.Fatal("panic failed to join and close")
	}
	p.Close()
}

// The first batch uses exactly the same bounds as later prepared batches, but
// it does not buy a helper until there is a batch to overlap with callbacks.
func TestListingPreparationShortTraversalStaysSynchronous(t *testing.T) {
	for _, count := range []int{0, 1, listingBatchNodes - 1} {
		p := newListingPreparation(nil)
		cursor := 0
		closed := false
		source := listingSource{
			size: func(int) (int, bool) { return 10, true },
			next: func() (data.NodeOrError, bool) {
				if cursor == count {
					return data.NodeOrError{}, false
				}
				cursor++
				return data.NodeOrError{Node: &data.Node{Type: data.NodeTypeFile}}, true
			},
			close: func() { closed = true },
		}
		got := 0
		for range p.wrap(context.Background(), source) {
			got++
		}
		if got != count || !closed || p.jobs != nil {
			t.Fatalf("count=%d got=%d closed=%v worker=%v", count, got, closed, p.jobs != nil)
		}
		p.Close()
		p.Close()
	}
}

func TestListingPreparationCancellationWaitsForPendingDecode(t *testing.T) {
	p := newListingPreparation(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	exited := make(chan struct{})
	var closed atomic.Bool
	cursor := 0
	source := listingSource{
		size:  func(int) (int, bool) { return 10, true },
		close: func() { closed.Store(true) },
		next: func() (data.NodeOrError, bool) {
			if cursor == listingBatchNodes {
				close(entered)
				<-release
				cursor++
				return data.NodeOrError{Error: errors.New("discarded pending error")}, true
			}
			cursor++
			return data.NodeOrError{Node: &data.Node{Type: data.NodeTypeFile}}, true
		},
	}
	go func() {
		defer close(exited)
		for range p.wrap(ctx, source) {
			<-entered
			cancel()
			break
		}
		p.Close()
	}()
	<-entered
	// The blocked pending read makes completion impossible until release. Use
	// an observable reader-close flag, not timing, to check ownership.
	if closed.Load() {
		t.Fatal("reader closed while decoder owns it")
	}
	select {
	case <-exited:
		t.Fatal("returned before pending decode settled")
	default:
	}
	close(release)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("pending decode did not join")
	}
	if !closed.Load() {
		t.Fatal("reader not closed after join")
	}
}

func TestListingPreparedUnconsumedReaderCanClose(t *testing.T) {
	loader := &listingTestLoader{payload: []byte(`{"nodes":[{"name":"a","type":"file"}]}`)}
	p := newListingPreparation(loader)
	defer p.Close()
	iterator, closeTree, err := p.loadOwned(context.Background(), restic.ID{})
	if err != nil {
		t.Fatal(err)
	}
	closeTree()
	closeTree()
	for range iterator {
		t.Fatal("closed unconsumed reader retained nodes")
	}
	if p.jobs != nil || loader.loads != 1 {
		t.Fatal("closing an unconsumed reader acquired work")
	}
}

func TestListingPreparedSkippedFramesAndLeaveFailure(t *testing.T) {
	trees, root := BuildTreeMap(TestTree{"a": TestTree{"x": TestFile{}}, "z": TestFile{}})
	sentinel := errors.New("callback failure")
	for _, where := range []string{"/", "/a", "leave:/a"} {
		for _, decision := range []error{ErrSkipNode, sentinel} {
			var want []string
			var wantErr error
			for _, prepared := range []bool{false, true} {
				var got []string
				visitor := WalkVisitor{
					ProcessNode: func(_ restic.ID, p string, _ *data.Node, err error) error {
						got = append(got, p)
						if p == where {
							return decision
						}
						return err
					},
					LeaveDir: func(p string) error {
						got = append(got, "leave:"+p)
						if "leave:"+p == where {
							return decision
						}
						return nil
					},
				}
				var err error
				if prepared {
					err = WalkPrepared(context.Background(), trees, root, visitor)
				} else {
					err = Walk(context.Background(), trees, root, visitor)
				}
				if !prepared {
					want, wantErr = got, err
				} else if !reflect.DeepEqual(want, got) || err != wantErr {
					t.Fatalf("%s: %v / %v; errors %v / %v", where, want, got, wantErr, err)
				}
			}
		}
	}
}
