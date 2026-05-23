// ABOUTME: Regression test for issue #5 - operator display name leak.
// ABOUTME: Verifies the served meeting page passes an explicit empty
// ABOUTME: userInfo.displayName so JaaS cannot default to the operator's
// ABOUTME: account-side identity.

package regression

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// RT-5.1 — TestMeetingPageDisplayNameOverride_NoJWT_RT5_1 verifies the
// served meeting page for a guest (no JWT) passes an explicit empty
// userInfo.displayName to the JitsiMeetExternalAPI constructor. Without
// this override, JaaS may default to the API-key owner's display name,
// leaking the operator's identity to every visitor (see issue #5).
func TestMeetingPageDisplayNameOverride_NoJWT_RT5_1(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	body := mustGetBody(t, ts.URL+"/some-room")

	if !containsDisplayNameOverride(body) {
		t.Errorf("served HTML for no-JWT request does not pass userInfo.displayName='' to JitsiMeetExternalAPI")
	}
}

// RT-5.2 — TestMeetingPageDisplayNameOverride_WithJWT_RT5_2 verifies the
// same override applies to JWT-bearing (moderator) visits. The moderator's
// JWT carries their preferred display name; the page must still explicitly
// clear the userInfo.displayName option so JaaS does not also inject an
// account-side default for guests sharing the same room.
func TestMeetingPageDisplayNameOverride_WithJWT_RT5_2(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	body := mustGetBody(t, ts.URL+"/some-room?jwt=fake-jwt-value")

	if !containsDisplayNameOverride(body) {
		t.Errorf("served HTML for JWT-bearing request does not pass userInfo.displayName='' to JitsiMeetExternalAPI")
	}
}

func mustGetBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	return string(raw)
}

// containsDisplayNameOverride checks for an explicit empty displayName option
// passed to the JaaS API constructor. Tolerant of whitespace and quoting style
// (single or double quotes) so the test does not pin a specific code shape.
func containsDisplayNameOverride(body string) bool {
	collapsed := strings.Join(strings.Fields(body), "")
	return strings.Contains(collapsed, `userInfo:{displayName:''}`) ||
		strings.Contains(collapsed, `userInfo:{displayName:""}`)
}
