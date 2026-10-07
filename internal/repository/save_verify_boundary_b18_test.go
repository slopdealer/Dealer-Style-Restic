package repository

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/restic"
)

// Admission uses the caller's size hint. Both routes must keep the complete
// content and terminal-error interpretation of the inherited verifier.
func TestSavedStreamAdmissionBoundaryB18(t *testing.T) {
	enc, err := zstd.NewWriter(nil, zstd.WithWindowSize(512<<10), zstd.WithEncoderCRC(true))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	for _, size := range []int{streamedSaveVerifyMinSize - 1, streamedSaveVerifyMinSize, streamedSaveVerifyMinSize + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			p := bytes.Repeat([]byte{41}, size)
			frame := enc.EncodeAll(p, nil)
			r := &Repository{key: crypto.NewRandomKey()}
			defer func() {
				if r.dec != nil {
					r.dec.Close()
				}
			}()
			buf := encryptSaveVerifyTest(r, frame)
			if err := r.verifyCiphertext(buf, size, restic.Hash(p)); err != nil {
				t.Fatal(err)
			}
			if eager := r.dec != nil; eager != (size < streamedSaveVerifyMinSize) {
				t.Fatalf("eager decoder initialized=%v at size %d", eager, size)
			}
			wrong := restic.Hash([]byte("wrong"))
			if got := r.verifyCiphertext(buf, size, wrong); got == nil || got.Error() != "hash mismatch" {
				t.Fatalf("wrong content: %v", got)
			}
			frame[len(frame)-1] ^= 1
			buf = encryptSaveVerifyTest(r, frame)
			got := r.verifyCiphertext(buf, size, wrong)
			want := eagerSaveVerifyTest(r, buf, size, wrong)
			if got == nil || fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("terminal failure versus wrong ID: got %v, want %v", got, want)
			}
		})
	}
}
