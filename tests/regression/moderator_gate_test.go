// ABOUTME: Regression tests for the moderator-route active gating and the
// ABOUTME: Login relabel (issue #14), which supersedes AC13.2.

package regression

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// --- AC14.1: moderator route is active-gated, same 404 as the guest gate ---

func TestModGate_ActiveShowsForm_RT14_1(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "live", f.now.Add(-time.Hour), f.now.Add(time.Hour))
	resp, body := f.doReq(t, "GET", "/live/moderator")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("active moderator route status=%d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "<h1>Login</h1>") {
		t.Errorf("active room did not render the login form; got %q", body[:min(200, len(body))])
	}
}

func TestModGate_UnknownInactive404_RT14_2(t *testing.T) {
	f := newGateFixture(t)
	resp, body := f.doReq(t, "GET", "/never/moderator")
	if resp.StatusCode != http.StatusNotFound || !bodyIsInactivePage(body) {
		t.Errorf("unknown moderator route: status=%d inactive=%v, want 404 inactive", resp.StatusCode, bodyIsInactivePage(body))
	}
}

func TestModGate_BeforeWindow404_RT14_3(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "soon", f.now.Add(time.Hour), f.now.Add(2*time.Hour))
	resp, body := f.doReq(t, "GET", "/soon/moderator")
	if resp.StatusCode != http.StatusNotFound || !bodyIsInactivePage(body) {
		t.Errorf("before-window moderator route not the inactive 404")
	}
}

func TestModGate_AfterWindow404_RT14_4(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "past", f.now.Add(-2*time.Hour), f.now.Add(-time.Hour))
	resp, body := f.doReq(t, "GET", "/past/moderator")
	if resp.StatusCode != http.StatusNotFound || !bodyIsInactivePage(body) {
		t.Errorf("after-window moderator route not the inactive 404")
	}
}

func TestModGate_Cancelled404_RT14_5(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "cx", f.now.Add(-time.Hour), f.now.Add(time.Hour))
	f.cancelRoom(t, "cx")
	resp, body := f.doReq(t, "GET", "/cx/moderator")
	if resp.StatusCode != http.StatusNotFound || !bodyIsInactivePage(body) {
		t.Errorf("cancelled moderator route not the inactive 404")
	}
}

func TestModGate_BlockedStatesIdentical_RT14_6(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "before", f.now.Add(time.Hour), f.now.Add(2*time.Hour))
	f.registerRoom(t, "after", f.now.Add(-2*time.Hour), f.now.Add(-time.Hour))
	f.registerRoom(t, "cxl", f.now.Add(-time.Hour), f.now.Add(time.Hour))
	f.cancelRoom(t, "cxl")

	read := func(slug string) (int, string) {
		resp, body := f.doReq(t, "GET", "/"+slug+"/moderator")
		return resp.StatusCode, body
	}
	us, ub := read("never")
	for _, slug := range []string{"before", "after", "cxl"} {
		s, b := read(slug)
		if s != us || b != ub {
			t.Errorf("%s/moderator differs from unknown blocked moderator response", slug)
		}
	}
}

func TestModGate_MatchesGuestInactive_RT14_7(t *testing.T) {
	f := newGateFixture(t)
	_, guest := f.doReq(t, "GET", "/never")
	_, mod := f.doReq(t, "GET", "/never/moderator")
	if guest != mod {
		t.Error("moderator inactive 404 body differs from the guest inactive page")
	}
}

func TestModGate_PostInactiveNoDelivery_RT14_8(t *testing.T) {
	f := newGateFixture(t)
	resp, body := f.doReq(t, "POST", "/never/moderator")
	if resp.StatusCode != http.StatusNotFound || !bodyIsInactivePage(body) {
		t.Errorf("POST to inactive moderator route: status=%d, want the inactive 404", resp.StatusCode)
	}
}

// --- AC14.2: Login relabel, no "moderator" in the visible copy ---

func TestModGate_LoginCopy_RT14_9(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "live", f.now.Add(-time.Hour), f.now.Add(time.Hour))
	_, body := f.doReq(t, "GET", "/live/moderator")
	if !strings.Contains(body, "<h1>Login</h1>") || !strings.Contains(body, "<title>Login</title>") {
		t.Error("entry page does not present the Login heading/title")
	}
	if strings.Contains(body, "Moderator access") {
		t.Error("entry page still contains the old 'Moderator access' copy")
	}
}

func TestModGate_VerifyCopy_RT14_10(t *testing.T) {
	f := newGateFixture(t)
	_, body := f.doReq(t, "GET", "/live/moderator/verify?token=bad")
	if strings.Contains(strings.ToLower(body), "moderator") {
		t.Error("failed-verify page copy still mentions moderator")
	}
	if !strings.Contains(body, "login link") {
		t.Errorf("failed-verify page does not use the login-link copy; got %q", body[:min(200, len(body))])
	}
}

func TestModGate_CheckEmailCopy_RT14_11(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "live", f.now.Add(-time.Hour), f.now.Add(time.Hour))
	resp := f.postForm(t, "/live/moderator", url.Values{"email": {"someone@example.com"}})
	if strings.Contains(strings.ToLower(resp), "moderator") {
		t.Error("check-email page copy still mentions moderator")
	}
	if !strings.Contains(resp, "login link") {
		t.Errorf("check-email page does not use the login-link copy; got %q", resp[:min(200, len(resp))])
	}
}

// postForm POSTs a form body to the given path and returns the response body.
func (f *fixture) postForm(t *testing.T, path string, form url.Values) string {
	t.Helper()
	resp, err := http.Post(f.ts.URL+path, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	return string(body[:n])
}
