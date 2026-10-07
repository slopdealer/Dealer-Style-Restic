package pack

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/repository/crypto"
)

func TestOwnedTailEntriesDifferential(t *testing.T) {
	key := crypto.NewRandomKey()
	cases := 0
	check := func(tail []byte) {
		t.Helper()
		cases++
		want, ws, we := ListOwnedTail(key, bytes.Clone(tail))
		owned := bytes.Clone(tail)
		entries, gs, ge := ListOwnedTailEntries(key, owned)
		if b11OwnedError(we) != b11OwnedError(ge) || ws != gs {
			t.Fatalf("case %d: size/error mismatch %d/%d %v/%v", cases, ws, gs, we, ge)
		}
		if ge != nil {
			if entries != nil {
				t.Fatal("failed validation published an iterator")
			}
			return
		}
		got := make(Blobs, 0)
		for entry := range entries {
			got = append(got, entry)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("case %d: entries differ", cases)
		}
		// Early termination must neither consume ownership nor change a replay.
		for range entries {
			break
		}
		again := make(Blobs, 0)
		for entry := range entries {
			again = append(again, entry)
		}
		if !reflect.DeepEqual(got, again) {
			t.Fatal("iterator replay changed values")
		}
		// Values already delivered are independent of the recycled input.
		clear(owned)
		if !reflect.DeepEqual(want, got) {
			t.Fatal("yielded values retained tail storage")
		}
	}
	for _, n := range []int{0, 1, 14, 15, 16, 1024, (MaxHeaderSize-headerSize)/int(entrySize) - 1} {
		check(b11OwnedEncrypt(key, b11OwnedPlain(n), 71))
	}
	for _, typ := range []byte{2, 3, 4, 255} {
		for _, length := range []int{0, 1, 4, 5, 36, 37, 38, 40, 41} {
			plain := append(b11OwnedPlain(1), make([]byte, length)...)
			if length > 0 {
				plain[len(plain)-length] = typ
			}
			check(b11OwnedEncrypt(key, plain, 71))
		}
	}
	for _, size := range []int{0, 3, 4, 35, 72, 73} {
		check(make([]byte, size))
	}
	for _, hlen := range []uint32{0, 1, 31, 32, 37, 70, 1024, ^uint32(0)} {
		tail := make([]byte, 128)
		binary.LittleEndian.PutUint32(tail[len(tail)-4:], hlen)
		check(tail)
	}
	auth := b11OwnedEncrypt(key, b11OwnedPlain(2), 71)
	auth[len(auth)-5] ^= 1
	check(auth)
	check(b11OwnedEncrypt(key, b11OwnedPlain(1), 71))
	t.Logf("matched %d cases: authentication, complete validation, offsets, early stop, replay and storage recycling", cases)
}

func BenchmarkOwnedTailEntries(b *testing.B) {
	key := crypto.NewRandomKey()
	for _, n := range []int{16, 4096} {
		tail := b11OwnedEncrypt(key, b11OwnedPlain(n), 71)
		for _, streaming := range []bool{false, true} {
			b.Run(fmt.Sprintf("entries=%d/iterator=%v", n, streaming), func(b *testing.B) {
				owned := make([]byte, len(tail))
				b.ReportAllocs()
				for b.Loop() {
					copy(owned, tail)
					if streaming {
						entries, _, err := ListOwnedTailEntries(key, owned)
						if err != nil {
							b.Fatal(err)
						}
						for entry := range entries {
							if entry.Type == 0 {
								b.Fatal("invalid entry")
							}
						}
					} else {
						entries, _, err := ListOwnedTail(key, owned)
						if err != nil {
							b.Fatal(err)
						}
						for _, entry := range entries {
							if entry.Type == 0 {
								b.Fatal("invalid entry")
							}
						}
					}
				}
			})
		}
	}
}

// A bounded local discriminator, run by the trusted test runner. This reports
// representation cost, not a repository-phase speedup.
func TestOwnedTailEntriesCost(t *testing.T) {
	key := crypto.NewRandomKey()
	for _, n := range []int{16, 4096} {
		tail := b11OwnedEncrypt(key, b11OwnedPlain(n), 71)
		for _, streaming := range []bool{false, true} {
			owned := make([]byte, len(tail))
			var last uint
			result := testing.Benchmark(func(b *testing.B) {
				for range b.N {
					copy(owned, tail)
					var total uint
					if streaming {
						entries, _, err := ListOwnedTailEntries(key, owned)
						if err != nil {
							b.Fatal(err)
						}
						for entry := range entries {
							total += entry.Length
						}
					} else {
						entries, _, err := ListOwnedTail(key, owned)
						if err != nil {
							b.Fatal(err)
						}
						for _, entry := range entries {
							total += entry.Length
						}
					}
					last = total
				}
			})
			if n > 0 && last == 0 {
				t.Fatal("entries were not consumed")
			}
			t.Logf("entries=%d iterator=%v ns/op=%d B/op=%d allocs/op=%d", n, streaming, result.NsPerOp(), result.AllocedBytesPerOp(), result.AllocsPerOp())
		}
	}
}
