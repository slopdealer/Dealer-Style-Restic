package data

import (
	"bytes"
	"testing"
)

// Finalize resets the embedded bytes.Buffer to its zero value. A subsequent
// value copy must not carry a writer bound to the original struct's buffer.
func TestTreeEncoderCopyAfterFinalize(t *testing.T) {
	got, want := NewTreeJSONBuilder(), testTreeNewMarshalBuilder()
	if err := got.AddNode(&Node{Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := want.add(&Node{Name: "a"}); err != nil {
		t.Fatal(err)
	}
	first, err := got.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, want.finish()) {
		t.Fatal("initial bytes differ")
	}
	owned := bytes.Clone(first)
	copied, copiedWant := *got, *want
	ge, we := copied.AddNode(&Node{Name: "b"}), copiedWant.add(&Node{Name: "b"})
	if testTreeError(ge) != testTreeError(we) || !bytes.Equal(copied.buf.Bytes(), copiedWant.buf.Bytes()) || !bytes.Equal(got.buf.Bytes(), want.buf.Bytes()) || !bytes.Equal(first, owned) {
		t.Fatalf("copy after Finalize differs: copied %q / %q, original %q / %q, errors %v / %v", copied.buf.Bytes(), copiedWant.buf.Bytes(), got.buf.Bytes(), want.buf.Bytes(), ge, we)
	}
}
