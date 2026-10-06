package index

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// Keep the pre-trial eager construction as an independent compatibility oracle.
func decodeIndexWithoutReservation(buf []byte, id restic.ID) (*Index, error) {
	var doc jsonIndex
	if err := json.Unmarshal(buf, &doc); err != nil {
		return nil, errors.Wrap(err, "DecodeIndex")
	}
	idx := NewIndex()
	for _, p := range doc.Packs {
		packID := idx.addToPacks(p.ID)
		for _, blob := range p.Blobs {
			idx.store(packID, pack.Blob{
				BlobHandle: restic.BlobHandle{Type: blob.Type, ID: blob.ID},
				Offset:     blob.Offset, Length: blob.Length,
				UncompressedLength: blob.UncompressedLength,
			})
		}
	}
	idx.ids = append(idx.ids, id)
	idx.final = true
	return idx, nil
}

func TestDecodeIndexReservationCompatibility(t *testing.T) {
	cases := diagnosticDecodeCases()
	var packs []string
	for i := 0; i < 2048; i++ {
		// Repeated blob IDs across different packs must keep every location.
		packs = append(packs, fmt.Sprintf(`{"id":"%064x","blobs":[{"id":"%064x","type":"data","offset":1,"length":33},{"id":"%064x","type":"tree","offset":34,"length":33}]}`, i+1, i%257+1, i%17+1))
	}
	cases = append(cases, `{"packs":[`+strings.Join(packs, ",")+`]}`)
	for i, src := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			buf := []byte(src)
			want := diagnosticDecodeOutcome(decodeIndexWithoutReservation, buf)
			got := diagnosticDecodeOutcome(DecodeIndex, buf)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("reserved=%+v original=%+v", got, want)
			}
		})
	}
}
