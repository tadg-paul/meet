// ABOUTME: Shared helpers used by multiple regression test files.

package regression

import (
	"testing"
	"time"
)

// mustRFC3339 parses an RFC3339 timestamp or fails the test.
func mustRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}
