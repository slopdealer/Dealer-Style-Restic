package index

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/restic/restic/internal/restic"
)

func TestCanonicalHexIDEveryByte(t *testing.T) {
	src := bytes.Repeat([]byte{'0'}, 64)
	for pos := range src {
		for ch := 0; ch < 256; ch++ {
			src[pos] = byte(ch)
			want := ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f'
			var got restic.ID
			if canonicalHexID(src, nil) != want || canonicalHexID(src, &got) != want {
				t.Fatalf("pos=%d byte=%d: wrong canonical acceptance", pos, ch)
			}
			if want {
				var decoded restic.ID
				_, _ = hex.Decode(decoded[:], src)
				if got != decoded {
					t.Fatalf("pos=%d byte=%d: wrong decode", pos, ch)
				}
			}
		}
		src[pos] = '0'
	}
	for _, length := range []int{0, 1, 63, 65, 128} {
		if canonicalHexID(make([]byte, length), nil) {
			t.Fatalf("accepted length %d", length)
		}
	}
}

func TestCanonicalHexDecoderCompatibility(t *testing.T) {
	inputs := diagnosticDecodeCases()
	var packs []string
	for i := 0; i < 2048; i++ {
		packs = append(packs, fmt.Sprintf(`{"id":"%064x","blobs":[{"id":"%064x","type":"data","offset":0,"length":4294967295},{"id":"%064x","type":"tree","offset":4294967295,"length":33,"uncompressed_length":4294967295}]}`, i+1, i%257, i%17))
	}
	canonical := `{"packs":[` + strings.Join(packs, ",") + `]}`
	if _, ok := decodeCanonicalIndex([]byte(canonical), restic.ID{}); !ok {
		t.Fatal("canonical fixture missed fast path")
	}
	inputs = append(inputs, canonical, canonical+"\n\t ", strings.ToUpper(canonical))
	// Every truncated prefix of a small, fully canonical index must retain the
	// ordinary decoder's errors rather than publish partial lookup storage.
	small := `{"packs":[` + packs[0] + `]}`
	for end := 0; end < len(small); end++ {
		inputs = append(inputs, small[:end])
	}
	for _, input := range inputs {
		got := diagnosticDecodeOutcome(decodeIndexEager, []byte(input))
		want := diagnosticDecodeOutcome(DecodeIndex, []byte(input))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("input=%q got=%+v want=%+v", input, got, want)
		}
	}
}
