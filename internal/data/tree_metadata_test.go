package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/restic/restic/internal/restic"
)

func TestMetadataTreeKeepsAllMetadata(t *testing.T) {
	id := restic.Hash([]byte("content"))
	stamp := time.Date(2020, 1, 2, 3, 4, 5, 6, time.FixedZone("offset", 1234))
	for _, typ := range []NodeType{NodeTypeFile, NodeTypeDir, NodeTypeSymlink, NodeTypeDev, NodeTypeFifo} {
		n := &Node{Name: "escaped\nname\\", Type: typ, Mode: 0640, ModTime: stamp,
			AccessTime: stamp, ChangeTime: stamp, UID: 123, GID: 456, User: "user", Group: "group",
			Inode: 8, DeviceID: 9, Size: 1024, Links: 2, LinkTarget: string([]byte{0xff, 0}),
			ExtendedAttributes: []ExtendedAttribute{{Name: "user.test", Value: []byte{0, 1, 2}}},
			GenericAttributes:  map[GenericAttributeType]json.RawMessage{"test": json.RawMessage(`{"value":1}`)},
			Device:             10, Content: restic.IDs{id, id}, Subtree: &id, Error: "recorded error"}
		payload, err := json.Marshal(struct {
			Nodes []*Node `json:"nodes"`
		}{[]*Node{n}})
		if err != nil {
			t.Fatal(err)
		}
		want, wantErr := metadataTreeResult(payload, false)
		got, gotErr := metadataTreeResult(payload, true)
		if wantErr != nil || gotErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: metadata=%v/%v ordinary=%v/%v", typ, got, gotErr, want, wantErr)
		}
	}
}

func TestMetadataTreeFramingAndEscapedContent(t *testing.T) {
	id := restic.Hash([]byte("blob")).String()
	contents := []string{`null`, `[]`, `[null]`, `[false]`, `[0]`, `[""]`,
		`["` + id + `"]`, `["\u` + fmt.Sprintf("%04x", id[0]) + id[1:] + `"]`,
		`["` + id + `",null]`, `["` + id + `","bad"]`}
	for _, content := range contents {
		for _, key := range []string{`content`, `CONTENT`, `c\u006fntent`} {
			raw := `{"name":"a","` + key + `":` + content + `,"size":1,"mode":384,"links":2,"inode":8,"device_id":9}`
			for _, payload := range []string{
				`{"nodes":[` + raw + `]}`,
				`{"unknown":null,"nodes":[` + raw + `,{"name":"b","content":[]}],"tail":{"nested":[]}}`,
				`{"nodes":[` + raw + `],"tail":`,
				`{"nodes":[` + raw + `,`,
			} {
				want, wantErr := metadataTreeResult([]byte(payload), false)
				got, gotErr := metadataTreeResult([]byte(payload), true)
				if !reflect.DeepEqual(got, want) || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) {
					t.Fatalf("%s: metadata=%v/%v ordinary=%v/%v", payload, got, gotErr, want, wantErr)
				}
			}
		}
	}
}

type metadataTestLoader struct {
	t   *testing.T
	id  restic.ID
	err error
}

func (l metadataTestLoader) LoadBlob(ctx context.Context, h restic.BlobHandle, buf []byte) ([]byte, error) {
	if h.Type != restic.TreeBlob || h.ID != l.id || buf != nil {
		l.t.Fatalf("unexpected authenticated load: %v, buffer %v", h, buf)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return []byte(`{"nodes":[{"name":"a","content":[]},{"name":"b","content":[]}]}`), l.err
}

func TestMetadataTreeLoadAndSingleUse(t *testing.T) {
	id := restic.Hash([]byte("tree"))
	loadErr := errors.New("authenticated load failed")
	it, err := LoadMetadataTree(context.Background(), metadataTestLoader{t: t, id: id, err: loadErr}, id)
	if it != nil || !errors.Is(err, loadErr) {
		t.Fatalf("load failure: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	it, err = LoadMetadataTree(ctx, metadataTestLoader{t: t, id: id}, id)
	if it != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled load: %v", err)
	}
	it, err = LoadMetadataTree(context.Background(), metadataTestLoader{t: t, id: id}, id)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for item := range it {
		if item.Error != nil || item.Node.Name != "a" {
			t.Fatalf("first node: %+v", item)
		}
		count++
		break
	}
	if count != 1 {
		t.Fatal("no node yielded")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("iterator allowed reuse after early stop")
		}
	}()
	for range it {
	}
}

func metadataTreeResult(payload []byte, metadata bool) ([]*Node, error) {
	load := newDirectTreeIterator
	if metadata {
		load = newMetadataTreeIterator
	}
	it, err := load(payload)
	if err != nil {
		return nil, err
	}
	var nodes []*Node
	for item := range it {
		if item.Error != nil {
			return nodes, item.Error
		}
		item.Node.Content = nil
		nodes = append(nodes, item.Node)
	}
	return nodes, nil
}

func TestMetadataTreeInterpretationAndErrors(t *testing.T) {
	id := restic.Hash([]byte("blob")).String()
	for _, raw := range []string{
		`{"name":"file","type":"file","content":["` + id + `"]}`,
		`{"name":"file","type":"file","CONTENT":["` + strings.ToUpper(id) + `"],"content":null}`,
		`{"name":"file","content":[` + " \n " + `"` + id + `" , "` + id + `"` + " \t " + `]}`,
		`{"name":"file","content":[]}`,
		`{"name":"file","content":null}`,
		`{"name":"a\\n","linktarget_raw":"AAE=","subtree":"` + id + `","unknown":[1,2]}`,
		`{"name":"file","content":[null]}`,
		`{"name":"file","content":["short"]}`,
		`{"name":"file","content":["` + strings.Repeat("z", 64) + `"]}`,
		`{"name":"file","content":123}`,
		`{"name":"file","content":{"bad":true}}`,
		`{"name":"file","size":"bad","content":["short"]}`,
		`{"name":"file","content":["short"],"size":"bad"}`,
		`{"name":"file","content":["` + id + `"],"content":[false]}`,
		`{"name":"file","content":["` + id + `",]}`,
		`{"name":"file","mtime":"bad","content":[]}`,
		`{"name":"bad\\q","content":[]}`,
		`{"name":"file","content":["` + id + `"]`,
	} {
		payload := []byte(`{"before":{},"nodes":[` + raw + `],"after":[]}`)
		want, wantErr := metadataTreeResult(payload, false)
		got, gotErr := metadataTreeResult(payload, true)
		if !reflect.DeepEqual(got, want) || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) {
			t.Fatalf("%s: metadata=%v/%v ordinary=%v/%v", raw, got, gotErr, want, wantErr)
		}
	}
}

func TestMetadataContentNoRetainedIDs(t *testing.T) {
	id := restic.Hash([]byte("blob"))
	payload, err := json.Marshal(struct {
		Nodes []*Node `json:"nodes"`
	}{[]*Node{{Name: "file", Type: NodeTypeFile, Content: restic.IDs{id, id}}}})
	if err != nil {
		t.Fatal(err)
	}
	it, err := newMetadataTreeIterator(payload)
	if err != nil {
		t.Fatal(err)
	}
	for item := range it {
		if item.Error != nil || item.Node.Content != nil {
			t.Fatalf("retained IDs or error: %+v", item)
		}
	}
}

func BenchmarkMetadataTreeDecode(b *testing.B) {
	for _, count := range []int{1, 32, 512} {
		ids := make(restic.IDs, count)
		for i := range ids {
			ids[i] = restic.Hash([]byte(fmt.Sprint(i)))
		}
		payload, err := json.Marshal(struct {
			Nodes []*Node `json:"nodes"`
		}{[]*Node{{Name: "file", Type: NodeTypeFile, Mode: 0644, Content: ids}}})
		if err != nil {
			b.Fatal(err)
		}
		for _, metadata := range []bool{false, true} {
			b.Run(fmt.Sprintf("ids=%d/metadata=%t", count, metadata), func(b *testing.B) {
				load := newDirectTreeIterator
				if metadata {
					load = newMetadataTreeIterator
				}
				b.ReportAllocs()
				for b.Loop() {
					it, err := load(payload)
					if err != nil {
						b.Fatal(err)
					}
					for item := range it {
						if item.Error != nil {
							b.Fatal(item.Error)
						}
					}
				}
			})
		}
	}
}
