// ABOUTME: CLI regression tests for moderator token flows introduced in issue #12.
// ABOUTME: Verifies super-moderator compatibility and room-scoped moderator links.

package regression

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func writeModeratorCLIConfig(t *testing.T, dir string, includeModeratorAuth bool) (string, *rsa.PrivateKey) {
	t.Helper()
	keyPEM := testPrivateKeyPEM(t)
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		t.Fatal("decode generated key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse generated key: %v", err)
	}
	key := parsed.(*rsa.PrivateKey)
	defaultsPath := filepath.Join(dir, "defaults.yaml")
	hostPath := filepath.Join(dir, "host.yaml")
	secretsPath := filepath.Join(dir, "secrets.yaml")
	if err := os.WriteFile(defaultsPath, []byte("base_url: https://defaults.example\n"), 0o600); err != nil {
		t.Fatalf("write defaults: %v", err)
	}
	if err := os.WriteFile(hostPath, []byte("base_url: https://meet.example\n"), 0o600); err != nil {
		t.Fatalf("write host config: %v", err)
	}
	secrets := "8x8-keys:\n" +
		"  app-id: vpaas-magic-cookie-test\n" +
		"  key-id: test-key\n" +
		"  private-key: |\n"
	for _, line := range strings.Split(strings.TrimRight(keyPEM, "\n"), "\n") {
		secrets += "    " + line + "\n"
	}
	if includeModeratorAuth {
		secrets += "moderator-auth:\n" +
			"  enabled: true\n" +
			"  magic-link-ttl: 15m\n" +
			"  moderator-jwt-ttl: 2h\n" +
			"  signing-key: cli-test-signing-key\n" +
			"  rooms:\n" +
			"    workshop:\n" +
			"      moderators:\n" +
			"        - mod@example.com\n"
	}
	if err := os.WriteFile(secretsPath, []byte(secrets), 0o600); err != nil {
		t.Fatalf("write secrets: %v", err)
	}
	return hostPath + "," + secretsPath, key
}

// AC12.7 — the existing token command remains an all-room super-moderator path.
func TestCLI_TokenSuperModeratorStillWildcard_RT12_27_RT12_28(t *testing.T) {
	dir := t.TempDir()
	configPaths, key := writeModeratorCLIConfig(t, dir, false)
	stdout, stderr, code := runMeet(t, dir, "token", "--config", configPaths, "--room", "workshop")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "https://meet.example/workshop?jwt=") {
		t.Fatalf("stdout=%q", stdout)
	}
	u, err := url.Parse(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatalf("parse token URL: %v", err)
	}
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(u.Query().Get("jwt"), claims, func(tok *jwt.Token) (interface{}, error) {
		return &key.PublicKey, nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("parse jwt valid=%v err=%v", parsed != nil && parsed.Valid, err)
	}
	if claims["room"] != "*" {
		t.Errorf("room=%v, want wildcard", claims["room"])
	}
}

// AC12.8 — authorised CLI room-scoped moderator links are printable and use
// the same config/secrets cascade.
func TestCLI_ModeratorLinkPrintsRoomScopedMagicLink_RT12_30(t *testing.T) {
	dir := t.TempDir()
	configPaths, _ := writeModeratorCLIConfig(t, dir, true)
	stdout, stderr, code := runMeet(t, dir,
		"moderator-link", "--config", configPaths,
		"--room", "workshop", "--email", "mod@example.com", "--print",
	)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "https://meet.example/workshop/moderator/verify?token=") {
		t.Errorf("stdout=%q", stdout)
	}
	if strings.Contains(stdout, "jwt=") {
		t.Errorf("magic link output contains final jwt: %q", stdout)
	}
}

// AC12.8 — unauthorised CLI pairs do not create verifier-accepted token state.
func TestCLI_ModeratorLinkRejectsUnauthorisedPair_RT12_31(t *testing.T) {
	dir := t.TempDir()
	configPaths, _ := writeModeratorCLIConfig(t, dir, true)
	stdout, stderr, code := runMeet(t, dir,
		"moderator-link", "--config", configPaths,
		"--room", "workshop", "--email", "other@example.com", "--print",
	)
	if code == 0 {
		t.Fatalf("unexpected success stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stderr, "not authorised") {
		t.Errorf("stderr=%q", stderr)
	}
	if csv := readModeratorLinksCSV(t, dir); strings.Contains(csv, "other@example.com") || strings.Contains(csv, "printed") {
		t.Errorf("unauthorised pair created usable state:\n%s", csv)
	}
}

// AC12.8 — a CLI-generated printed magic link verifies to a room-scoped JWT.
func TestCLI_PrintedModeratorLinkProducesRoomJWT_RT12_32(t *testing.T) {
	dir := t.TempDir()
	configPaths, key := writeModeratorCLIConfig(t, dir, true)
	stdout, stderr, code := runMeet(t, dir,
		"moderator-link", "--config", configPaths,
		"--room", "workshop", "--email", "mod@example.com", "--print",
	)
	if code != 0 {
		t.Fatalf("moderator-link exit=%d stderr=%q", code, stderr)
	}
	verifyURL, err := url.Parse(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	stdout, stderr, code = runMeet(t, dir,
		"moderator-verify", "--config", configPaths,
		"--room", "workshop", "--token", verifyURL.Query().Get("token"),
	)
	if code != 0 {
		t.Fatalf("moderator-verify exit=%d stderr=%q", code, stderr)
	}
	u, err := url.Parse(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatalf("parse moderator URL: %v", err)
	}
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(u.Query().Get("jwt"), claims, func(tok *jwt.Token) (interface{}, error) {
		return &key.PublicKey, nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("parse jwt valid=%v err=%v", parsed != nil && parsed.Valid, err)
	}
	if claims["room"] != "workshop" {
		t.Errorf("room=%v, want workshop", claims["room"])
	}
}

func readModeratorLinksCSV(t *testing.T, stateDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(stateDir, "moderator_links.csv"))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read moderator_links.csv: %v", err)
	}
	return string(data)
}
