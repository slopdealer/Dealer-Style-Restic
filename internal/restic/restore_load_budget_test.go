package restic

import "testing"

func TestBoundedRestoreBlobWorkingSet(t *testing.T) {
	for _, tt := range []struct {
		plain, cipher    uint
		compressed, want bool
	}{
		{1 << 20, 1<<20 + 64, true, true},
		{1<<20 + 1, 1 << 20, true, false},
		{1 << 20, 1<<20 + 65, true, false},
		{3 << 20, 3<<20 + 64, false, true},
		{3<<20 + 1, 3 << 20, false, false},
		{3 << 20, 3<<20 + 65, false, false},
	} {
		if got := BoundedRestoreBlob(tt.plain, tt.cipher, tt.compressed); got != tt.want {
			t.Fatalf("%+v: got %v", tt, got)
		}
	}
}
