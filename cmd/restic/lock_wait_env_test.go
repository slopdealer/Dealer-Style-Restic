package main

import (
	"os"
	"testing"
	"time"
)

// testLockWait returns the lock's settle delay for the integration tests.
//
// The default is zero, as in stock: the tests then do not spend 200 ms per
// command. With a delay of zero the lock code starts no wait work at all, so
// nothing of what overlaps the settle delay (the early snapshot listing and
// index load, check's prepared pack sizes, ls's prepared snapshot) is covered.
// To run the same tests the way the binary runs, set the production value:
//
//	RESTIC_TEST_LOCK_WAIT=200ms go test ./cmd/restic
//
// Tests that set their own delay with repository.TestSetLockTimeout keep it.
func testLockWait(t testing.TB) time.Duration {
	value := os.Getenv("RESTIC_TEST_LOCK_WAIT")
	if value == "" {
		return 0
	}
	wait, err := time.ParseDuration(value)
	if err != nil || wait < 0 {
		t.Fatalf("RESTIC_TEST_LOCK_WAIT=%q is not a duration such as 200ms", value)
	}
	return wait
}
