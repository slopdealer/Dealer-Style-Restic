package repository

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestAutoDataBlobLooksCompressible(t *testing.T) {
	repo := &Repository{}

	compressible := bytes.Repeat([]byte("restic-compression-sample-"), 1<<15)
	if !repo.autoDataBlobLooksCompressible(compressible) {
		t.Fatal("repeated data was classified as incompressible")
	}

	incompressible := make([]byte, 1<<20)
	rng := rand.New(rand.NewSource(1))
	if _, err := rng.Read(incompressible); err != nil {
		t.Fatal(err)
	}
	if repo.autoDataBlobLooksCompressible(incompressible) {
		t.Fatal("pseudorandom data was classified as compressible")
	}
}
