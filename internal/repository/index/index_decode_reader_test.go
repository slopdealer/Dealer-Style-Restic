package index

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/restic"
)

// Short and terminal-error readers exercise publication only after full settlement.
type diagnosticShortReader struct {
	io.Reader
	limit int
}

func (r diagnosticShortReader) Read(p []byte) (int, error) {
	if len(p) > r.limit {
		p = p[:r.limit]
	}
	return r.Reader.Read(p)
}

type diagnosticErrorReader struct{ data []byte }

func (r *diagnosticErrorReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, fmt.Errorf("terminal stream error")
	}
	return n, nil
}

func diagnosticPerPackAccepted(rd io.Reader) bool {
	d := json.NewDecoder(rd)
	for _, want := range []any{json.Delim('{'), "packs", json.Delim('[')} {
		if tok, err := d.Token(); err != nil || tok != want {
			return false
		}
	}
	for d.More() {
		var p packJSON
		if err := d.Decode(&p); err != nil {
			return false
		}
	}
	for _, want := range []any{json.Delim(']'), json.Delim('}')} {
		if tok, err := d.Token(); err != nil || tok != want {
			return false
		}
	}
	var extra json.RawMessage
	return d.Decode(&extra) == io.EOF
}

func TestDecodeIndexReaderCompatibility(t *testing.T) {
	id := restic.Hash([]byte("reader identity"))
	for _, input := range diagnosticDecodeCases() {
		for _, size := range []int{1, 7, 1024} {
			decode := func(buf []byte, id restic.ID) (*Index, error) {
				idx, ok := DecodeIndexReader(diagnosticShortReader{bytes.NewReader(buf), size}, id)
				if !ok {
					if idx != nil {
						panic("published partial reader index")
					}
					return DecodeIndex(buf, id)
				}
				return idx, nil
			}
			got := diagnosticDecodeOutcome(decode, []byte(input))
			want := diagnosticDecodeOutcome(DecodeIndex, []byte(input))
			if !reflect.DeepEqual(got, want) {
				panic("streaming decoder differs on " + input)
			}
		}
	}
	for _, n := range []int{9996, 9997, 9998, 10000} {
		input := `{"packs":[{"ignored":` + strings.Repeat("[", n) + `0` + strings.Repeat("]", n) + `}]}`
		_, eagerErr := DecodeIndex([]byte(input), id)
		_, guardedOK := DecodeIndexReader(diagnosticShortReader{strings.NewReader(input), 7}, id)
		if guardedOK != (eagerErr == nil) {
			panic(fmt.Sprintf("depth %d: eager=%v guarded=%v", n, eagerErr, guardedOK))
		}
	}
	for _, input := range []string{`{"packs":[]}`, `{"packs":[]} `, `{"packs":[]}true`} {
		if idx, ok := DecodeIndexReader(&diagnosticErrorReader{data: []byte(input)}, id); ok || idx != nil {
			panic("reader accepted terminal error")
		}
	}
	if _, ok := decodeIndexReaderLimited(strings.NewReader(`{"packs":[]}`+strings.Repeat(" ", 100)), id, 64); ok {
		panic("cumulative byte limit not enforced")
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithWindowSize(1024))
	if err != nil {
		panic(err)
	}
	first := enc.EncodeAll([]byte(`{"packs":[]}`+strings.Repeat(" ", 600000)), nil)
	frames := append(first, enc.EncodeAll([]byte(strings.Repeat(" ", 600000)), nil)...)
	enc.Close()
	opts := []zstd.DOption{zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(1024 * 1024)}
	dec, err := zstd.NewReader(nil, opts...)
	if err != nil {
		panic(err)
	}
	if _, err := dec.DecodeAll(frames, nil); err == nil {
		panic("DecodeAll did not enforce cumulative output ceiling")
	}
	dec.Close()
	dec, err = zstd.NewReader(bytes.NewReader(frames), opts...)
	if err != nil {
		panic(err)
	}
	if _, ok := DecodeIndexReader(dec, id); !ok {
		panic("streaming did not expose lower-limit cumulative discrepancy")
	}
	dec.Close()
	dec, err = zstd.NewReader(bytes.NewReader(frames), opts...)
	if err != nil {
		panic(err)
	}
	if _, ok := decodeIndexReaderLimited(dec, id, 1024*1024); ok {
		panic("guarded reader accepted oversized concatenated output")
	}
	dec.Close()
}

func TestDecodeIndexReaderDepthEngine(t *testing.T) {
	for _, n := range []int{9997, 9998} {
		input := `{"packs":[{"ignored":` + strings.Repeat("[", n) + `0` + strings.Repeat("]", n) + `}]}`
		_, err := DecodeIndex([]byte(input), restic.ID{})
		t.Logf("n=%d eager=%v unguarded=%v", n, err, diagnosticPerPackAccepted(strings.NewReader(input)))
	}
}

func FuzzDecodeIndexReaderCompatibility(f *testing.F) {
	for _, input := range diagnosticDecodeCases() {
		f.Add([]byte(input))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 65536 {
			t.Skip()
		}
		decode := func(buf []byte, id restic.ID) (*Index, error) {
			idx, ok := DecodeIndexReader(diagnosticShortReader{bytes.NewReader(buf), 7}, id)
			if !ok {
				if idx != nil {
					t.Fatal("partial index")
				}
				return DecodeIndex(buf, id)
			}
			return idx, nil
		}
		got := diagnosticDecodeOutcome(decode, input)
		want := diagnosticDecodeOutcome(DecodeIndex, input)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("input=%q got=%+v want=%+v", input, got, want)
		}
	})
}

func TestDecodeIndexReaderFastPathAndSettlement(t *testing.T) {
	id := restic.Hash([]byte("identity"))
	inputs := diagnosticDecodeCases()[:8]
	blob := `{"id":"` + strings.Repeat("2", 64) + `","type":"tree","offset":5,"length":31}`
	p := `{"id":"` + strings.Repeat("1", 64) + `","blobs":[` + blob + `]}`
	inputs = append(inputs, `{"packs":[`+strings.Repeat(p+",", 999)+p+`]}`)
	p = `{"id":"` + strings.Repeat("1", 64) + `","blobs":[` + strings.Repeat(blob+",", 999) + blob + `]}`
	inputs = append(inputs, `{"packs":[`+p+`,{"blobs":[{}]}]}`)
	for _, input := range inputs {
		idx, ok := DecodeIndexReader(diagnosticShortReader{strings.NewReader(input), 7}, id)
		if !ok || idx == nil {
			t.Fatalf("supported shape missed stream path: %.200s", input)
		}
		decode := func([]byte, restic.ID) (*Index, error) { return idx, nil }
		// The outcome helper uses a fixed identity, so compare with that identity.
		want := diagnosticDecodeOutcome(DecodeIndex, []byte(input))
		idx.ids = want.ids
		got := diagnosticDecodeOutcome(decode, nil)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("pack result differs: got=%+v want=%+v", got, want)
		}
	}
	representative := inputs[0]
	for end := range len(representative) {
		if idx, ok := DecodeIndexReader(strings.NewReader(representative[:end]), id); ok || idx != nil {
			t.Fatalf("accepted truncation %d", end)
		}
	}
	// Neither a long trailer nor an error delivered only after it can complete a
	// partial or invalid index. The whitespace scan does not use Decode(&extra).
	trailer := `{"packs":[]}` + strings.Repeat(" \n\r\t", 1<<18)
	if _, ok := DecodeIndexReader(strings.NewReader(trailer), id); !ok {
		t.Fatal("valid whitespace trailer rejected")
	}
	if idx, ok := DecodeIndexReader(&diagnosticErrorReader{data: []byte(trailer)}, id); ok || idx != nil {
		t.Fatal("late terminal error ignored")
	}
}
