package data

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
)

func TestParentFinderDifferential(t *testing.T) {
	for i, payload := range parentCaseFixtures() {
		before := bytes.Clone(payload)
		iterator, oldErr := newParentTreeIterator(payload)
		next, nextErr := NewParentTreeFinder(payload)
		if parentCaseFacts(oldErr) != parentCaseFacts(nextErr) {
			t.Fatalf("case%d initialization: %v / %v", i, oldErr, nextErr)
		}
		if oldErr != nil {
			continue
		}
		old := NewTreeFinder(iterator)
		for _, name := range []string{"", "a", "a", "aa", "b", "c", "z", "zz", "zzz"} {
			a, ae := old.Find(name)
			b, be := next.Find(name)
			if !reflect.DeepEqual(a, b) || parentCaseFacts(ae) != parentCaseFacts(be) {
				t.Fatalf("case%d Find(%q): nodes=%+v / %+v errors=%+v / %+v", i, name, a, b, parentCaseFacts(ae), parentCaseFacts(be))
			}
		}
		old.Close()
		next.Close()
		old.Close()
		next.Close()
		if !bytes.Equal(payload, before) {
			t.Fatalf("case%d mutated input", i)
		}
	}
}

func TestParentFinderCloseDoesNotReadTail(t *testing.T) {
	payload := []byte(`{"nodes":[{"name":"b"},{"mode":"bad"}]}`)
	for _, direct := range []bool{false, true} {
		var finder *TreeFinder
		var err error
		if direct {
			finder, err = NewParentTreeFinder(payload)
		} else {
			var it TreeNodeIterator
			it, err = newParentTreeIterator(payload)
			finder = NewTreeFinder(it)
		}
		if err != nil {
			t.Fatal(err)
		}
		n, err := finder.Find("a")
		if n != nil || err != nil {
			t.Fatalf("lookahead: %+v %v", n, err)
		}
		finder.Close()
		n, err = finder.Find("b")
		if n == nil || n.Name != "b" || err != nil {
			t.Fatalf("close changed lookahead: %+v %v", n, err)
		}
		n, err = finder.Find("c")
		if n != nil || err != nil {
			t.Fatalf("close parsed malformed tail: %+v %v", n, err)
		}
	}
}

// This isolates lookup and interpretation from metadata observations and tree
// reconstruction. It is not a command-level speed measurement.
func BenchmarkParentFinderBoundary(b *testing.B) {
	builder := NewTreeJSONBuilder()
	for i := 0; i < 256; i++ {
		if err := builder.AddNode(&Node{Name: fmt.Sprintf("%04d", i), Type: NodeTypeFile}); err != nil {
			b.Fatal(err)
		}
	}
	payload, err := builder.Finalize()
	if err != nil {
		b.Fatal(err)
	}
	for _, direct := range []bool{false, true} {
		b.Run(fmt.Sprintf("direct=%v", direct), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var finder *TreeFinder
				if direct {
					finder, err = NewParentTreeFinder(payload)
				} else {
					var it TreeNodeIterator
					it, err = newParentTreeIterator(payload)
					finder = NewTreeFinder(it)
				}
				if err != nil {
					b.Fatal(err)
				}
				for j := 0; j < 256; j++ {
					n, err := finder.Find(fmt.Sprintf("%04d", j))
					if n == nil || err != nil {
						b.Fatalf("lookup %d: %v", j, err)
					}
				}
				finder.Close()
			}
		})
	}
}
