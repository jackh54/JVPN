package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureCreatesArtifacts(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "tls.crt")
	key := filepath.Join(dir, "tls.key")
	tok := filepath.Join(dir, "token")
	if err := Ensure(dir, cert, key, tok); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{cert, key, tok} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}
	if err := Ensure(dir, cert, key, tok); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
}
