package data

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/restic/restic/internal/restic"
)

// Independently frozen M003-1 wire schema and normalization. Do not replace
// this reference with json.Marshal(Node), which would call the candidate.
type scalarFrozenWire struct {
	Name       string      `json:"name"`
	Type       NodeType    `json:"type"`
	Mode       os.FileMode `json:"mode,omitempty"`
	ModTime    time.Time   `json:"mtime"`
	AccessTime time.Time   `json:"atime"`
	ChangeTime time.Time   `json:"ctime"`
	UID        uint32      `json:"uid"`
	GID        uint32      `json:"gid"`
	User       string      `json:"user,omitempty"`
	Group      string      `json:"group,omitempty"`
	Inode      uint64      `json:"inode,omitempty"`
	DeviceID   uint64      `json:"device_id,omitempty"` // device id of the file, stat.st_dev, only stored for hardlinks
	Size       uint64      `json:"size,omitempty"`
	Links      uint64      `json:"links,omitempty"`
	LinkTarget string      `json:"linktarget,omitempty"`
	// implicitly base64-encoded field. Only used while encoding, `linktarget_raw` will overwrite LinkTarget if present.
	// This allows storing arbitrary byte-sequences, which are possible as symlink targets on unix systems,
	// as LinkTarget without breaking backwards-compatibility.
	// Must only be set of the linktarget cannot be encoded as valid utf8.
	LinkTargetRaw      []byte                                   `json:"linktarget_raw,omitempty"`
	ExtendedAttributes []ExtendedAttribute                      `json:"extended_attributes,omitempty"`
	GenericAttributes  map[GenericAttributeType]json.RawMessage `json:"generic_attributes,omitempty"`
	Device             uint64                                   `json:"device,omitempty"` // in case of Type == "dev", stat.st_rdev
	Content            restic.IDs                               `json:"content"`
	Subtree            *restic.ID                               `json:"subtree,omitempty"`

	Error string `json:"error,omitempty"`

	Path string `json:"-"`
}

func scalarFrozenTime(t time.Time) time.Time {
	switch {
	case t.Year() < 0000:
		return t.AddDate(-t.Year(), 0, 0)
	case t.Year() > 9999:
		return t.AddDate(-(t.Year() - 9999), 0, 0)
	default:
		return t
	}
}

func scalarFrozenMarshal(node Node) ([]byte, error) {
	// make sure invalid timestamps for mtime and atime are converted to
	// something we can actually save.
	node.ModTime = scalarFrozenTime(node.ModTime)
	node.AccessTime = scalarFrozenTime(node.AccessTime)
	node.ChangeTime = scalarFrozenTime(node.ChangeTime)

	type nodeJSON scalarFrozenWire
	nj := nodeJSON(node)
	name := strconv.Quote(node.Name)
	nj.Name = name[1 : len(name)-1]
	if nj.LinkTargetRaw != nil {
		panic("LinkTargetRaw must not be set manually")
	}
	if !utf8.ValidString(node.LinkTarget) {
		// store raw bytes if invalid utf8
		nj.LinkTargetRaw = []byte(node.LinkTarget)
	}

	return json.Marshal(nj)
}

func scalarCompare(t interface{ Fatalf(string, ...any) }, node Node) {
	before := scalarFrozenWire(node)
	got, ge := node.MarshalJSON()
	want, we := scalarFrozenMarshal(node)
	if fmt.Sprint(ge) != fmt.Sprint(we) || !bytes.Equal(got, want) {
		t.Fatalf("node=%+v\ngot=%s err=%v\nwant=%s err=%v", node, got, ge, want, we)
	}
	if !reflect.DeepEqual(before, scalarFrozenWire(node)) {
		t.Fatalf("input modified")
	}
	after, ae := scalarFrozenMarshal(node)
	if !bytes.Equal(after, want) || fmt.Sprint(ae) != fmt.Sprint(we) {
		t.Fatalf("input storage modified")
	}
}

func scalarCases(t interface{ Fatalf(string, ...any) }) int {
	id := restic.Hash([]byte("content"))
	base := Node{Name: "alpha", Type: NodeTypeFile, Mode: 0644, UID: 1000, GID: 1000, User: "user", Group: "group", Inode: 123, DeviceID: 99, Size: 19, Links: 1, Content: restic.IDs{id}, ModTime: time.Date(2024, 5, 6, 7, 8, 9, 123456789, time.UTC)}
	base.AccessTime, base.ChangeTime = base.ModTime, base.ModTime
	cases := []Node{{}, {Name: "a", Content: restic.IDs{}}, base, {Name: "directory", Type: NodeTypeDir, Subtree: &id}, {Name: "a", Device: ^uint64(0), Mode: ^os.FileMode(0), Size: ^uint64(0), Links: ^uint64(0), Inode: ^uint64(0), DeviceID: ^uint64(0), UID: ^uint32(0), GID: ^uint32(0)}, {Name: "a", ExtendedAttributes: []ExtendedAttribute{}, GenericAttributes: map[GenericAttributeType]json.RawMessage{}}, {Name: "a", ExtendedAttributes: []ExtendedAttribute{{Name: "nil"}, {Name: "empty", Value: []byte{}}, {Name: "bytes", Value: []byte{0, 255}}}}, {Name: "a", GenericAttributes: map[GenericAttributeType]json.RawMessage{"z": []byte(` {"a":1,"a":2} `), "a": []byte(` ["<&>", null] `)}}, {Name: "a", GenericAttributes: map[GenericAttributeType]json.RawMessage{"bad": []byte(`{"a":`)}}, {Name: "a", GenericAttributes: map[GenericAttributeType]json.RawMessage{"bad": []byte(`true false`)}}}
	for _, s := range []string{"", "quote\"", "slash\\", "control\n\t\x00", "<&>", "\u2028\u2029", "\xff\xfe", "日本語", strings.Repeat("a", 4096), strings.Repeat("b", 4097)} {
		for field := range 6 {
			n := base
			switch field {
			case 0:
				n.Name = s
			case 1:
				n.Type = NodeType(s)
			case 2:
				n.User = s
			case 3:
				n.Group = s
			case 4:
				n.LinkTarget = s
			case 5:
				n.Error = s
			}
			cases = append(cases, n)
		}
	}
	for _, year := range []int{-12000, -1, 0, 1, 9999, 10000, 12000} {
		for _, off := range []int{-86460, -86400, -86399, -3601, -1, 0, 1, 3601, 86399, 86400, 86460} {
			for _, ns := range []int{0, 1, 1000, 100000000, 999999999} {
				n := base
				n.ModTime = time.Date(year, 2, 29, 4, 5, 6, ns, time.FixedZone("custom", off))
				n.AccessTime = n.ModTime.Add(time.Second)
				n.ChangeTime = n.ModTime.Add(-time.Second)
				cases = append(cases, n)
			}
		}
	}
	for _, count := range []int{0, 1, 2, 32, 4096, 4097} {
		n := base
		n.Content = make(restic.IDs, count)
		for i := range n.Content {
			n.Content[i] = restic.Hash([]byte(fmt.Sprint(i)))
		}
		cases = append(cases, n)
	}
	rng := rand.New(rand.NewSource(71))
	for range 500 {
		n := base
		n.Mode = os.FileMode(rng.Uint32())
		n.UID = rng.Uint32()
		n.GID = rng.Uint32()
		n.Inode = rng.Uint64()
		n.DeviceID = rng.Uint64()
		n.Size = rng.Uint64()
		n.Links = rng.Uint64()
		n.Device = rng.Uint64()
		n.Name = fmt.Sprintf("node-%x", rng.Uint64())
		cases = append(cases, n)
	}
	for _, n := range cases {
		scalarCompare(t, n)
	}
	for _, raw := range [][]byte{{}, {1}} {
		n := base
		n.LinkTargetRaw = raw
		panicOf := func(f func() ([]byte, error)) (p any) { defer func() { p = recover() }(); _, _ = f(); return }
		if !reflect.DeepEqual(panicOf(n.MarshalJSON), panicOf(func() ([]byte, error) { return scalarFrozenMarshal(n) })) {
			t.Fatalf("raw link panic differs")
		}
	}
	return len(cases)
}

func TestScalarNodeFrozenBytes(t *testing.T) {
	t.Logf("executed %d independent frozen cases", scalarCases(t))
}

func scalarOwnership(t interface{ Fatalf(string, ...any) }) {
	id := restic.Hash([]byte("original"))
	n := Node{Name: "a", Type: NodeTypeFile, Content: restic.IDs{id}, Subtree: &id}
	b, err := n.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	owned := bytes.Clone(b)
	n.Content[0] = restic.ID{}
	*n.Subtree = restic.ID{}
	n.Name = "changed"
	for range 10 {
		_, _ = n.MarshalJSON()
	}
	if !bytes.Equal(b, owned) {
		t.Fatalf("owned scalar output changed")
	}
	saver := &scalarRetainingSaver{}
	w := NewTreeWriter(saver)
	n.Name = "a"
	if err := w.AddNode(&n); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := w.Finalize(context.Background()); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	owned = bytes.Clone(saver.buf)
	n.Content[0] = id
	*n.Subtree = id
	n.Name = "z"
	if err := w.AddNode(&n); err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if !bytes.Equal(saver.buf, owned) {
		t.Fatalf("delayed saver storage changed")
	}
	// Compare complete tree bytes without invoking the candidate on the reference.
	builder := NewTreeJSONBuilder()
	var frozen bytes.Buffer
	frozen.WriteString(`{"nodes":[`)
	for i, name := range []string{"a", "b", "c"} {
		n.Name = name
		if err := builder.AddNode(&n); err != nil {
			t.Fatalf("tree: %v", err)
		}
		if i > 0 {
			frozen.WriteByte(',')
		}
		value, err := scalarFrozenMarshal(n)
		if err != nil {
			t.Fatalf("frozen: %v", err)
		}
		frozen.Write(value)
	}
	frozen.WriteString("]}\n")
	actual, err := builder.Finalize()
	if err != nil || !bytes.Equal(actual, frozen.Bytes()) {
		t.Fatalf("independent tree bytes differ")
	}
}

func TestScalarNodeOwnedOutputAndTree(t *testing.T) { scalarOwnership(t) }

// Delayed consumer deliberately retains the finalized payload.
type scalarRetainingSaver struct{ buf []byte }

func (s *scalarRetainingSaver) SaveBlob(_ context.Context, _ restic.BlobType, b []byte, _ restic.ID, _ bool) (restic.ID, bool, int, error) {
	s.buf = b
	return restic.Hash(b), false, len(b), nil
}
