package repository

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/restic"
)

func TestCoalescedPackReplay(t *testing.T) {
	for _, cut := range []int{0, 100, 432, 600, 864, 1100} {
		t.Run(string(rune('a'+cut/100)), func(t *testing.T) {
			key := testKey(t)
			blobs, body := buildPackfileWithoutHeader([]int{400, 400, 400, 400}, &key, false)
			dec, err := zstd.NewReader(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer dec.Close()
			calls, attempts, delivered := 0, 0, 0
			load := func(ctx context.Context, _ backend.Handle, length int, offset int64, consume func(io.Reader) error) error {
				calls++
				attempts++
				if err := consume(bytes.NewReader(body[offset : int(offset)+cut])); err == nil {
					t.Fatal("partial response unexpectedly succeeded")
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				attempts++
				return consume(bytes.NewReader(body[offset : int(offset)+length]))
			}
			err = streamPackWithPolicy(t.Context(), load, nil, dec, &key, restic.ID{}, blobs,
				func(h restic.BlobHandle, p []byte, err error) error {
					if err != nil {
						return err
					}
					if delivered >= len(blobs) || h != blobs[delivered].BlobHandle || restic.Hash(p) != h.ID {
						t.Fatal("callback repeated, reordered or corrupted")
					}
					delivered++
					return nil
				}, 900, false)
			if err != nil || calls != 1 || attempts != 2 || delivered != len(blobs) {
				t.Fatalf("err %v, calls %d, attempts %d, delivered %d", err, calls, attempts, delivered)
			}
		})
	}
}

func TestCoalescedPackTerminalFailure(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "exhausted", true: "retry"}[retry], func(t *testing.T) {
			key := testKey(t)
			blobs, body := buildPackfileWithoutHeader([]int{400, 400, 400, 400}, &key, false)
			dec, err := zstd.NewReader(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer dec.Close()
			terminal := errors.New("terminal close failure")
			delivered, attempts := 0, 0
			load := func(_ context.Context, _ backend.Handle, length int, offset int64, consume func(io.Reader) error) error {
				attempts++
				if err := consume(bytes.NewReader(body[offset : int(offset)+length])); err != nil {
					return err
				}
				if delivered == len(blobs) {
					t.Fatal("final delivery preceded response completion")
				}
				if retry {
					attempts++
					return consume(bytes.NewReader(body[offset : int(offset)+length]))
				}
				return terminal
			}
			err = streamPackWithPolicy(t.Context(), load, nil, dec, &key, restic.ID{}, blobs,
				func(h restic.BlobHandle, p []byte, err error) error {
					if err != nil {
						return err
					}
					if h != blobs[delivered].BlobHandle || restic.Hash(p) != h.ID {
						t.Fatal("wrong callback")
					}
					delivered++
					return nil
				}, 900, false)
			if retry {
				if err != nil || delivered != 4 || attempts != 2 {
					t.Fatalf("err %v, delivered %d, attempts %d", err, delivered, attempts)
				}
			} else if !errors.Is(err, terminal) || delivered != 2 {
				t.Fatalf("err %v, delivered %d", err, delivered)
			}
		})
	}
}

func TestCoalescedPackRejectAndRecover(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "recover", true: "reject"}[reject], func(t *testing.T) {
			key := testKey(t)
			blobs, body := buildPackfileWithoutHeader([]int{400, 400, 400, 400}, &key, false)
			dec, err := zstd.NewReader(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer dec.Close()
			original := append([]byte(nil), body...)
			if !reject {
				body[0] ^= 1
			}
			active, delivered, recoveries := false, 0, 0
			load := func(ctx context.Context, _ backend.Handle, length int, offset int64, consume func(io.Reader) error) error {
				if active {
					t.Fatal("nested connection")
				}
				active = true
				defer func() { active = false }()
				err := consume(bytes.NewReader(body[offset : int(offset)+length]))
				if err != nil && ctx.Err() == nil {
					return consume(bytes.NewReader(body[offset : int(offset)+length]))
				}
				return err
			}
			recoverBlob := func(_ context.Context, h restic.BlobHandle, _ []byte) ([]byte, error) {
				if active {
					t.Fatal("recovery while connection held")
				}
				recoveries++
				blob := blobs[0]
				cipher := append([]byte(nil), original[blob.Offset:blob.Offset+blob.Length]...)
				return key.Open(cipher[key.NonceSize():key.NonceSize()], cipher[:key.NonceSize()], cipher[key.NonceSize():], nil)
			}
			rejected := errors.New("handler rejected after observable work")
			err = streamPackWithPolicy(t.Context(), load, recoverBlob, dec, &key, restic.ID{}, blobs,
				func(h restic.BlobHandle, p []byte, err error) error {
					if err != nil {
						return err
					}
					if delivered >= len(blobs) || h != blobs[delivered].BlobHandle || restic.Hash(p) != h.ID {
						t.Fatal("wrong callback")
					}
					delivered++
					if reject {
						return rejected
					}
					return nil
				}, 900, false)
			if reject {
				if !errors.Is(err, rejected) || delivered != 1 || recoveries != 0 {
					t.Fatalf("err %v, delivered %d, recovery %d", err, delivered, recoveries)
				}
			} else if err != nil || delivered != 4 || recoveries != 1 {
				t.Fatalf("err %v, delivered %d, recovery %d", err, delivered, recoveries)
			}
		})
	}
}

func TestCoalescedPackExhaustionKeepsWindowBound(t *testing.T) {
	key := testKey(t)
	blobs, body := buildPackfileWithoutHeader([]int{400, 400, 400, 400}, &key, false)
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	var data []byte
	loads, delivered, recoveries := 0, 0, 0
	active := false
	load := func(_ context.Context, _ backend.Handle, length int, offset int64, consume func(io.Reader) error) error {
		loads++
		active = true
		defer func() { active = false }()
		if err := consume(bytes.NewReader(body[offset : int(offset)+length])); err != nil {
			return err
		}
		return errors.New("permanent terminal failure")
	}
	recovery := func(_ context.Context, h restic.BlobHandle, _ []byte) ([]byte, error) {
		if active {
			t.Fatal("recovery owns a live connection")
		}
		recoveries++
		for _, blob := range blobs {
			if blob.BlobHandle == h {
				cipher := append([]byte(nil), body[blob.Offset:blob.Offset+blob.Length]...)
				return key.Open(cipher[key.NonceSize():key.NonceSize()], cipher[:key.NonceSize()], cipher[key.NonceSize():], nil)
			}
		}
		t.Fatal("unknown recovery blob")
		return nil, nil
	}
	err = streamPackResponse(t.Context(), load, recovery, dec, &key, restic.ID{}, blobs, &data,
		func(h restic.BlobHandle, plaintext []byte, err error) error {
			if err != nil {
				return err
			}
			if cap(data) > 900 || h != blobs[delivered].BlobHandle || restic.Hash(plaintext) != h.ID {
				t.Fatal("window exceeded, or delivery repeated")
			}
			delivered++
			return nil
		}, 900)
	if err != nil || loads != 1 || recoveries != 2 || delivered != 4 {
		t.Fatalf("err %v, loads %d, recoveries %d, delivered %d", err, loads, recoveries, delivered)
	}
}
