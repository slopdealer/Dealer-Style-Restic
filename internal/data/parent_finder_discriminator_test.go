package data

import (
	"fmt"
	"testing"
)

// Compare the shipped bounded route, including its duplicate initialization,
// with the same ordered lookups. Fixture/name construction is outside timing.
func BenchmarkParentFinderAdmission(b *testing.B) {
	for _, count := range []int{16, 256, 1024} {
		names := make([]string, count)
		builder := NewTreeJSONBuilder()
		for j := range names {
			names[j] = fmt.Sprintf("file-%06d", j)
			if err := builder.AddNode(&Node{Name: names[j], Type: NodeTypeFile}); err != nil {
				b.Fatal(err)
			}
		}
		payload, err := builder.Finalize()
		if err != nil {
			b.Fatal(err)
		}
		for _, mode := range []string{"iterator", "direct", "admitted-double-init"} {
			b.Run(fmt.Sprintf("%d/%s", count, mode), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					var finder *TreeFinder
					if mode == "iterator" || mode == "admitted-double-init" {
						it, e := newParentTreeIterator(payload)
						if e != nil {
							b.Fatal(e)
						}
						if mode == "iterator" {
							finder = NewTreeFinder(it)
						}
					}
					if mode != "iterator" {
						finder, err = NewParentTreeFinder(payload)
						if err != nil {
							b.Fatal(err)
						}
					}
					for _, name := range names {
						n, e := finder.Find(name)
						if e != nil || n == nil || n.Name != name {
							b.Fatalf("lookup %q: %v", name, e)
						}
					}
					finder.Close()
				}
			})
		}
	}
}
