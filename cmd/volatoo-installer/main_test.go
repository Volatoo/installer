package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTrustedKeyDirectory(t *testing.T) {
	directory := t.TempDir()
	name := strings.Repeat("a", 64) + ".pub"
	if err := os.WriteFile(filepath.Join(directory, name), []byte("untrusted comment: test\nRWRmYWtl\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	documents, err := readTrustedKeyDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 {
		t.Fatalf("got %d trusted keys, want 1", len(documents))
	}
}

func TestReadTrustedKeyDirectoryRejectsUnexpectedEntry(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "README"), []byte("not a key"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readTrustedKeyDirectory(directory); err == nil {
		t.Fatal("trusted keyring accepted an unexpected entry")
	}
}

func TestReadTrustedKeyDirectoryRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("key"), 0o644); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(directory, strings.Repeat("b", 64)+".pub")
	if err := os.Symlink(target, name); err != nil {
		t.Fatal(err)
	}
	if _, err := readTrustedKeyDirectory(directory); err == nil {
		t.Fatal("trusted keyring accepted a symlink")
	}
}
