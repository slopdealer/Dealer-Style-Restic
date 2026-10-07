package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/restic"
)

// The ordinary iterator is unchanged production code. This cohort corrupts
// framing and individual values to compare scanner and semantic settlement.
func TestB06ParentCorruptionSettlement(t *testing.T) {
	seeds := [][]byte{
		[]byte(`{"nodes":[{"name":"a","type":"file","mode":420,"uid":10,"gid":20,"content":[]},{"name":"b","mtime":"2000-01-01T00:00:00Z","linktarget_raw":"Yg=="}]}`),
		[]byte(`{"before":[1,true,null],"nodes":[null,{"name":"a","generic_attributes":{"x":[1,2]},"extended_attributes":[{"name":"a","value":"AA=="}]}],"after":{"key":"value"}}`),
		[]byte(`{"nodes":[{"name":"a","uid":1,"uid":"bad","content":["bad"],"mtime":"bad"}]}`),
	}
	rng := rand.New(rand.NewSource(72391))
	facts := func(err error) string {
		if err == nil {
			return ""
		}
		s := fmt.Sprintf("%T:%s", err, err)
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			s += fmt.Sprintf(":syntax=%d", syntax.Offset)
		}
		var typed *json.UnmarshalTypeError
		if errors.As(err, &typed) {
			s += fmt.Sprintf(":type=%s:offset=%d:field=%s:struct=%s", typed.Type, typed.Offset, typed.Field, typed.Struct)
		}
		return s
	}
	read := func(payload []byte, direct bool) (nodes []*Node, err error) {
		var it TreeNodeIterator
		if direct {
			it, err = newParentTreeIterator(payload)
		} else {
			it, err = NewTreeNodeIterator(bytes.NewReader(payload))
		}
		if err == nil {
			for item := range it {
				if item.Error != nil {
					return nodes, item.Error
				}
				nodes = append(nodes, item.Node)
			}
		}
		return
	}
	for i := 0; i < 30000; i++ {
		payload := bytes.Clone(seeds[i%len(seeds)])
		for j := 0; j < 1+rng.Intn(4); j++ {
			pos := rng.Intn(len(payload))
			payload[pos] = byte(rng.Intn(256))
		}
		if i%7 == 0 {
			payload = payload[:rng.Intn(len(payload)+1)]
		}
		want, we := read(payload, false)
		got, ge := read(payload, true)
		if !reflect.DeepEqual(want, got) || facts(we) != facts(ge) {
			t.Fatalf("case=%d payload=%q\nordinary=%v\nparent=%v", i, payload, facts(we), facts(ge))
		}
	}
	t.Log("executed 30000 framing/value corruption comparisons")
	// Keep the public loader entry point covered with a cancelled input loader.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := LoadParentTree(ctx, &parentCaseLoader{err: context.Canceled}, restic.ID{})
	if err != context.Canceled {
		t.Fatal(err)
	}
}
