package data

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/restic/restic/internal/restic"
)

// The direct tree decoder (LoadTree, LoadTreeNodeReader) skips the
// strconv.Unquote of a node name where it changes nothing. It must accept and
// reject exactly the trees that stock's decoder accepts and rejects, with the
// same names and the same error. Stock's decoder is NewTreeNodeIterator, which
// decodes every node with Node.UnmarshalJSON.

// The rule itself: a name for which the call is skipped is a name that
// strconv.Unquote returns unchanged.
func TestNodeNameIsPlainMeansUnquoteIsIdentity(t *testing.T) {
	check := func(name string) {
		if !nodeNameIsPlain(name) {
			return
		}
		got, err := strconv.Unquote(`"` + name + `"`)
		if err != nil || got != name {
			t.Fatalf("name %q counts as plain, but strconv.Unquote returns %q, %v", name, got, err)
		}
	}
	// every byte alone, and inside ASCII and inside a multi-byte character
	for c := 0; c < 256; c++ {
		b := string([]byte{byte(c)})
		for _, name := range []string{b, "a" + b, b + "a", "a" + b + "b", "\xc3" + b, b + "\xa9", "é" + b + "é"} {
			check(name)
		}
	}
	alphabet := []string{"", "a", "Z", " ", "\x00", "\x01", "\t", "\n", "\r", "\x1f", "\x7f", `"`, `\`, `'`, "`",
		"\x80", "\xbf", "\xc3", "\xa9", "\xe2", "\x82", "\xac", "\xf0", "\xff", "é", "€", "😀", "\ufffd", "\u2028", "n", "x", "u", "0"}
	for _, a := range alphabet {
		for _, b := range alphabet {
			for _, c := range alphabet {
				check(a + b + c)
			}
		}
	}
	rnd := rand.New(rand.NewSource(7))
	plain := 0
	for i := 0; i < 200000; i++ {
		var name strings.Builder
		for n := rnd.Intn(12); n > 0; n-- {
			if rnd.Intn(3) == 0 {
				name.WriteByte(byte(rnd.Intn(256)))
			} else {
				name.WriteString(alphabet[rnd.Intn(len(alphabet))])
			}
		}
		if nodeNameIsPlain(name.String()) {
			plain++
		}
		check(name.String())
	}
	if plain == 0 {
		t.Fatal("no random name was plain")
	}

	// the names the rule is for
	for _, name := range []string{"", "file.txt", "a b", "Ünïcödé", "日本語", "a'b", "tilde~", "\x7f"} {
		if !nodeNameIsPlain(name) {
			t.Errorf("name %q must not need strconv.Unquote", name)
		}
	}
	for _, name := range []string{`a"b`, "a\nb", `a\\b`, `\x41`, "a\tb", "\x00", "a\xffb", "\xc3"} {
		if nodeNameIsPlain(name) {
			t.Errorf("name %q must go through strconv.Unquote", name)
		}
	}
}

type decodedTree struct {
	names []string
	err   string
}

type payloadLoader []byte

func (l payloadLoader) LoadBlob(_ context.Context, _ restic.BlobHandle, _ []byte) ([]byte, error) {
	return bytes.Clone(l), nil
}

func collectTree(it TreeNodeIterator, err error) (d decodedTree) {
	if err != nil {
		d.err = err.Error()
		return d
	}
	for item := range it {
		if item.Error != nil {
			d.err = item.Error.Error()
			return d
		}
		d.names = append(d.names, item.Node.Name)
	}
	return d
}

func decodeTreeStock(payload []byte) decodedTree {
	return collectTree(NewTreeNodeIterator(bytes.NewReader(payload)))
}

func decodeTreeLoadTree(payload []byte) decodedTree {
	return collectTree(LoadTree(context.Background(), payloadLoader(payload), restic.Hash(payload)))
}

func decodeTreeNodeReader(payload []byte) (d decodedTree) {
	rd, err := LoadTreeNodeReader(context.Background(), payloadLoader(payload), restic.Hash(payload))
	if err != nil {
		d.err = err.Error()
		return d
	}
	for {
		item, ok := rd.Next()
		if !ok {
			return d
		}
		if item.Error != nil {
			d.err = item.Error.Error()
			return d
		}
		d.names = append(d.names, item.Node.Name)
	}
}

func equalDecodedTrees(a, b decodedTree) bool {
	if a.err != b.err || len(a.names) != len(b.names) {
		return false
	}
	for i := range a.names {
		if a.names[i] != b.names[i] {
			return false
		}
	}
	return true
}

// treeWithNames builds a tree document whose node names are the given JSON
// string contents, taken as they are (they may be invalid JSON).
func treeWithNames(jsonNames ...string) []byte {
	var buf bytes.Buffer
	buf.WriteString(`{"nodes":[`)
	for i, name := range jsonNames {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(`{"name":"` + name + `","type":"file","content":[]}`)
	}
	buf.WriteString("]}\n")
	return buf.Bytes()
}

func compareTreeDecoders(t *testing.T, payload []byte) (rejected bool) {
	t.Helper()
	stock := decodeTreeStock(payload)
	if got := decodeTreeLoadTree(payload); !equalDecodedTrees(stock, got) {
		t.Fatalf("tree %q:\nstock    names=%q err=%q\nLoadTree names=%q err=%q", payload, stock.names, stock.err, got.names, got.err)
	}
	if got := decodeTreeNodeReader(payload); !equalDecodedTrees(stock, got) {
		t.Fatalf("tree %q:\nstock          names=%q err=%q\nTreeNodeReader names=%q err=%q", payload, stock.names, stock.err, got.names, got.err)
	}
	return stock.err != ""
}

// Pieces of a JSON string, as they stand between the quotes of "name".
var jsonNamePieces = []string{
	// plain text
	"a", "b c", "Z9", "é", "日本", "😀", "'", "`", "/", "~",
	// raw bytes that are not valid UTF-8
	"\xff", "\xc3", "\x80", "\xe2\x82",
	// raw control bytes: not allowed inside a JSON string
	"\x01", "\n", "\t",
	// JSON escapes that leave a raw character and no backslash in the name
	`\"`, `\n`, `\t`, `\r`, `\b`, `\f`, `\/`,
	`\u0000`, `\u0001`, `\u000a`, `\u001f`, `\u0020`, `\u0022`, `\u007f`, `\u0080`, `\u00e9`, `\u2028`,
	`\ud800`, `\udc00`, `\ud83d\ude00`,
	// JSON escapes that leave a backslash in the name: the second layer,
	// which restic writes with strconv.Quote
	`\\`, `\u005c`, `\\\\`, `\\\"`, `\\"`, `\\n`, `\\t`, `\\a`, `\\v`, `\\'`, `\\q`,
	`\\x41`, `\\xff`, `\\x4`, `\\x`, `\\u00e9`, `\\u12`, `\\U0001f600`, `\\U00110000`, `\\101`, `\\777`, `\\8`,
	// broken JSON escapes
	`\x`, `\u12`, `\`,
}

func TestTreeDecodersAgreeWithStockOnAdversarialNames(t *testing.T) {
	// the two names of the review, and what restic writes for a quote
	for _, name := range []string{`a\"b`, `a\nb`} {
		if !compareTreeDecoders(t, treeWithNames(name)) {
			t.Errorf("stock accepts the name %s: the test does not test what it should", name)
		}
	}
	if compareTreeDecoders(t, treeWithNames(`a\\\"b`)) {
		t.Error("stock rejects a quote as restic writes it")
	}

	accepted, rejected := 0, 0
	count := func(wasRejected bool) {
		if wasRejected {
			rejected++
		} else {
			accepted++
		}
	}
	// every piece alone, every pair, every pair around plain text
	for _, a := range jsonNamePieces {
		count(compareTreeDecoders(t, treeWithNames(a)))
		for _, b := range jsonNamePieces {
			count(compareTreeDecoders(t, treeWithNames(a+b)))
			count(compareTreeDecoders(t, treeWithNames(a+"x"+b)))
		}
	}
	// random names, also behind and in front of good nodes
	rnd := rand.New(rand.NewSource(20261006))
	for i := 0; i < 30000; i++ {
		names := make([]string, 1+rnd.Intn(3))
		for j := range names {
			var name strings.Builder
			for n := rnd.Intn(7); n > 0; n-- {
				name.WriteString(jsonNamePieces[rnd.Intn(len(jsonNamePieces))])
			}
			names[j] = name.String()
		}
		count(compareTreeDecoders(t, treeWithNames(names...)))
	}
	if accepted < 1000 || rejected < 1000 {
		t.Fatalf("one-sided sample: %d trees accepted, %d rejected", accepted, rejected)
	}
	t.Logf("%d trees accepted and %d rejected by stock, the direct decoders agree on all", accepted, rejected)
}

// Names of every kind, written the way restic writes them: all decoders
// return the name.
func TestTreeDecodersAgreeWithStockOnWrittenNames(t *testing.T) {
	rnd := rand.New(rand.NewSource(11))
	pieces := []string{"a", "file", " ", ".", "é", "日本", "😀", `"`, `\`, "\n", "\t", "\x00", "\x1f", "\x7f", "\xff", "\xc3", "\x80", "\u2028", "'"}
	for i := 0; i < 5000; i++ {
		nodes := make([]*Node, 1+rnd.Intn(4))
		want := make([]string, len(nodes))
		for j := range nodes {
			var name strings.Builder
			for n := rnd.Intn(8); n > 0; n-- {
				if rnd.Intn(4) == 0 {
					name.WriteByte(byte(rnd.Intn(256)))
				} else {
					name.WriteString(pieces[rnd.Intn(len(pieces))])
				}
			}
			want[j] = name.String()
			nodes[j] = &Node{Name: want[j], Type: NodeTypeFile}
		}
		var buf bytes.Buffer
		buf.WriteString(`{"nodes":[`)
		for j, node := range nodes {
			if j > 0 {
				buf.WriteByte(',')
			}
			encoded, err := json.Marshal(node)
			if err != nil {
				t.Fatal(err)
			}
			buf.Write(encoded)
		}
		buf.WriteString("]}\n")

		if compareTreeDecoders(t, buf.Bytes()) {
			t.Fatalf("stock rejects a tree that restic wrote: %q", buf.Bytes())
		}
		got := decodeTreeLoadTree(buf.Bytes())
		for j := range want {
			if got.names[j] != want[j] {
				t.Fatalf("name %q was read back as %q", want[j], got.names[j])
			}
		}
	}
}
