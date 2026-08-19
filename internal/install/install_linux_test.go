package install

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const testAuthorizedKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIN8XFE9WwjHvSxBSnbiuupCmyvRetPJYcHARXeTwdtLb installer-test\n"

func TestLoadSSHKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authorized_keys")
	if err := os.WriteFile(path, []byte(testAuthorizedKey), 0o600); err != nil {
		t.Fatal(err)
	}
	content, err := loadSSHKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != testAuthorizedKey {
		t.Fatalf("loaded key differs: %q", content)
	}
}

func TestLoadSSHKeysRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	link := filepath.Join(directory, "authorized_keys")
	if err := os.WriteFile(target, []byte(testAuthorizedKey), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSSHKeys(link); err == nil {
		t.Fatal("loadSSHKeys accepted a symlink")
	}
}

func TestLoadSSHKeysRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authorized_keys")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSSHKeys(path); err == nil {
		t.Fatal("loadSSHKeys accepted an oversized file")
	}
}
