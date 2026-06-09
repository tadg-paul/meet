// ABOUTME: Regression tests for NixOS config-path discovery.
// ABOUTME: Verifies defaults.yaml is resolved beside CONFIG_PATH.

package regression

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// AC11.1 — when CONFIG_PATH is supplied by deploy-nix, defaults.yaml is loaded
// from the same directory as CONFIG_PATH before the host-specific config.
func TestConfigPaths_DefaultsBesideConfigPath_RT11_1(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.yaml")
	defaultsPath := filepath.Join(configDir, "defaults.yaml")

	keyPEM := testPrivateKeyPEM(t)
	defaults := "base_url: https://defaults.example\n" +
		"8x8-keys:\n" +
		"  app-id: test-app\n" +
		"  key-id: test-key\n" +
		"  private-key: |\n"
	for _, line := range strings.Split(strings.TrimRight(keyPEM, "\n"), "\n") {
		defaults += "    " + line + "\n"
	}
	if err := os.WriteFile(defaultsPath, []byte(defaults), 0o600); err != nil {
		t.Fatalf("write defaults: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("base_url: https://host.example\n"), 0o600); err != nil {
		t.Fatalf("write host config: %v", err)
	}

	stdout, stderr, code := runMeetWithEnv(t, []string{
		"CONFIG_PATH=" + configPath,
		"SECRETS_PATH=",
	}, "token", "--room", "demo")
	if code != 0 {
		t.Fatalf("meet token exit=%d stderr=%q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "https://host.example/demo?jwt=") {
		t.Errorf("token URL=%q, want host config base_url overriding defaults", stdout)
	}
}

// AC11.2 — local development without CONFIG_PATH preserves the existing
// repository-relative defaults path.
func TestConfigPaths_LocalDefaultsWhenNoConfigPath_RT11_2(t *testing.T) {
	stdout, stderr, code := runMeetWithEnv(t, []string{
		"CONFIG_PATH=",
		"SECRETS_PATH=",
	}, "token", "--room", "demo")
	if code == 0 {
		t.Fatalf("meet token unexpectedly succeeded with only local defaults; stdout=%q", stdout)
	}
	if !strings.Contains(stderr, "app-id not configured") {
		t.Errorf("stderr=%q, want local config/defaults.yaml to load without app-id", stderr)
	}
}

func runMeetWithEnv(t *testing.T, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(meetBin, args...)
	cmd.Env = append(os.Environ(), env...)
	var sout, serr bytes.Buffer
	cmd.Stdout = &sout
	cmd.Stderr = &serr
	err := cmd.Run()
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("exec meet: %v", err)
	}
	return sout.String(), serr.String(), exitCode
}

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}
