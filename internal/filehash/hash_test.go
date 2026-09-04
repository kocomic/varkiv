package filehash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryFingerprintIsStableAndContentSensitive(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.bin"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "a.bin"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstHash, firstSize, err := Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, secondSize, err := Directory(root)
	if err != nil || secondHash != firstHash || secondSize != firstSize || firstSize != int64(len("second")+len("first")) {
		t.Fatalf("unstable directory fingerprint: %q/%d %q/%d err=%v", firstHash, firstSize, secondHash, secondSize, err)
	}
	if err = os.WriteFile(filepath.Join(root, "nested", "a.bin"), []byte("FIRST"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedHash, changedSize, err := Directory(root)
	if err != nil || changedHash == firstHash || changedSize != firstSize {
		t.Fatalf("same-size content drift was not detected: %q/%d err=%v", changedHash, changedSize, err)
	}
}

func TestDirectoryFingerprintRejectsSymbolicLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret.bin")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link.bin")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := Directory(root); err == nil {
		t.Fatal("symlinked directory member was accepted")
	}
}
