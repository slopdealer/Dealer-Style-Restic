package index

import (
 "bytes"
 "io"
 "reflect"
 "strings"
 "testing"

 "github.com/restic/restic/internal/restic"
)

func TestCanonicalStreamCompatibility(t *testing.T) {
 for _, input := range diagnosticDecodeCases() {
  for _, size := range []int{1, 7, 65536} {
   decode := func(buf []byte, id restic.ID) (*Index, error) {
    idx, ok := DecodeCanonicalIndexStream(func() (io.ReadCloser, error) {
     return io.NopCloser(diagnosticShortReader{bytes.NewReader(buf), size}), nil
    }, id)
    if !ok { if idx != nil { t.Fatal("partial index escaped") }; return DecodeIndex(buf, id) }
    return idx, nil
   }
   got := diagnosticDecodeOutcome(decode, []byte(input))
   want := diagnosticDecodeOutcome(DecodeIndex, []byte(input))
   if !reflect.DeepEqual(got, want) { t.Fatalf("input=%q got=%+v want=%+v", input, got, want) }
  }
 }
}

type canonicalTestCloser struct { io.Reader; close func() }
func (c *canonicalTestCloser) Close() error { c.close(); return nil }

func TestCanonicalStreamOnePassSettlement(t *testing.T) {
 input := `{"packs":[]}` + strings.Repeat(" ",100000)
 id := restic.Hash([]byte("one pass"))
 for _, terminal := range []bool{false,true} {
  var rd io.Reader = strings.NewReader(input)
  if terminal { rd = &diagnosticErrorReader{data: []byte(input)} }
  closes := 0
  idx,ok := DecodeCanonicalIndexStream(func()(io.ReadCloser,error){return &canonicalTestCloser{Reader: rd,close: func(){closes++}},nil},id)
  if ok == terminal || (idx != nil) == terminal || closes != 1 { t.Fatalf("terminal=%v idx=%v ok=%v closes=%d",terminal,idx,ok,closes) }
 }
}

func TestCanonicalStreamBoundAndTruncation(t *testing.T) {
 input := `{"packs":[]}`
 id := restic.Hash([]byte("limit"))
 for end := range len(input) {
  idx,ok := DecodeCanonicalIndexStream(func()(io.ReadCloser,error){return io.NopCloser(strings.NewReader(input[:end])),nil},id)
  if ok || idx != nil { t.Fatalf("accepted truncation %d",end) }
 }
 idx,ok := decodeCanonicalIndexStreamLimited(func()(io.ReadCloser,error){return io.NopCloser(strings.NewReader(input+strings.Repeat(" ",100))),nil},id,64)
 if ok || idx != nil { t.Fatal("accepted cumulative output excess") }
}
