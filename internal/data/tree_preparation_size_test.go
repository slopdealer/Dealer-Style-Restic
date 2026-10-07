package data

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/restic/restic/internal/restic"
)

func TestTreePreparationAdmissionDoesNotInterpret(t *testing.T) {
	for _, raw := range []string{
		`{"name":"a","type":"file"}`,
		`{"name":"[\\\"}]","type":"file","unknown":[{"x":"}"}]}`,
		`{"name":1}`,
		`{"name":"a","content":["` + strings.Repeat("0", 64) + `"]}`,
	} {
		r, err := LoadTreeNodeReader(context.Background(), readerTestLoader(`{"nodes":[`+raw+`]}`), restic.ID{})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := r.PreparationSize(len(raw) - 1); ok {
			t.Fatal("admitted over budget")
		}
		size, ok := r.PreparationSize(len(raw))
		if !ok || size != len(raw) {
			t.Fatalf("wrong frame size: %d %v for %s", size, ok, raw)
		}
		item, exists := r.Next()
		ordinary, _ := LoadTree(context.Background(), readerTestLoader(`{"nodes":[`+raw+`]}`), restic.ID{})
		for want := range ordinary {
			if !exists || fmt.Sprintf("%#v:%T:%v", item.Node, item.Error, item.Error) != fmt.Sprintf("%#v:%T:%v", want.Node, want.Error, want.Error) {
				t.Fatal("admission changed decode")
			}
		}
		r.Close()
		if _, ok := r.PreparationSize(2048); ok {
			t.Fatal("closed reader admitted work")
		}
	}
}
