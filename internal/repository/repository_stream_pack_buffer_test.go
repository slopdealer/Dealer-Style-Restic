package repository

import "testing"

func TestStreamPackBufferPoolClearsAndBoundsBuffers(t *testing.T) {
	originalPool := streamPackBufferPool
	streamPackBufferPool = make(chan []byte, 2)
	defer func() {
		streamPackBufferPool = originalPool
	}()
	putStreamPackBuffer(nil)
	if len(streamPackBufferPool) != 0 {
		t.Fatal("pool retained a zero-capacity buffer")
	}

	buf := make([]byte, 3, 8)
	full := buf[:cap(buf)]
	for i := range full {
		full[i] = 0xa5
	}
	putStreamPackBuffer(buf)

	got := takeStreamPackBuffer()
	if len(got) != 0 || cap(got) != cap(buf) {
		t.Fatalf("takeStreamPackBuffer() returned len/cap %d/%d, want 0/%d", len(got), cap(got), cap(buf))
	}
	for i, value := range got[:cap(got)] {
		if value != 0 {
			t.Fatalf("buffer byte %d = %#x after pooling, want zero", i, value)
		}
	}

	for i := 0; i < 3; i++ {
		putStreamPackBuffer(make([]byte, 0, 1))
	}
	if len(streamPackBufferPool) != cap(streamPackBufferPool) {
		t.Fatalf("pool retained %d buffers, want its bounded capacity %d", len(streamPackBufferPool), cap(streamPackBufferPool))
	}
	_ = takeStreamPackBuffer()
	_ = takeStreamPackBuffer()
	if got := takeStreamPackBuffer(); got != nil {
		t.Fatal("pool retained more buffers than its bounded capacity")
	}

	putStreamPackBuffer(make([]byte, maxStreamPackChunkSize))
	if got := takeStreamPackBuffer(); cap(got) != maxStreamPackChunkSize {
		t.Fatalf("pool rejected an ordinary max-sized range buffer: cap %d", cap(got))
	}

	putStreamPackBuffer(make([]byte, maxStreamPackChunkSize+1))
	if got := takeStreamPackBuffer(); got != nil {
		t.Fatal("one-off oversized range buffer was retained")
	}
}
