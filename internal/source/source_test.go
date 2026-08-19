package source

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/Volatoo/installer/internal/release"
)

func TestResolveLocalArtifact(t *testing.T) {
	base := t.TempDir()
	index := filepath.Join(base, "channels", "dev", "index.json")
	resolved, err := Resolve(index, "../../objects/sha256/aa/object")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(base, "objects", "sha256", "aa", "object") {
		t.Fatalf("unexpected content-addressed object path: %s", resolved)
	}
	resolved, err = Resolve(index, "objects/archive")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(base, "channels", "dev", "objects", "archive") {
		t.Fatalf("unexpected resolved path: %s", resolved)
	}
}

func TestFetchBlobLocal(t *testing.T) {
	base := t.TempDir()
	content := []byte("authenticated artifact")
	object := filepath.Join(base, "object")
	if err := os.WriteFile(object, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	blob := release.Blob{URL: "object", Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:]), Format: "zstd"}
	destination := t.TempDir()
	fetched, err := FetchBlob(filepath.Join(base, "index.json"), blob, destination, "archive")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(fetched)
	if err != nil || string(actual) != string(content) {
		t.Fatalf("unexpected fetched content: %q, %v", actual, err)
	}
	blob.SHA256 = "b" + blob.SHA256[1:]
	if _, err := FetchBlob(filepath.Join(base, "index.json"), blob, t.TempDir(), "changed"); err == nil {
		t.Fatal("changed artifact was accepted")
	}
}

func TestSnapshotBlobRejectsSymlink(t *testing.T) {
	base := t.TempDir()
	content := []byte("authenticated artifact")
	object := filepath.Join(base, "object")
	if err := os.WriteFile(object, content, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(object, link); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	blob := release.Blob{Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:]), Format: "zstd"}
	if _, err := SnapshotBlob(link, blob, t.TempDir(), "archive"); err == nil {
		t.Fatal("symlink artifact was accepted")
	}
}

func TestSnapshotBlobIsIndependentOfSource(t *testing.T) {
	base := t.TempDir()
	content := []byte("authenticated artifact")
	object := filepath.Join(base, "object")
	if err := os.WriteFile(object, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	blob := release.Blob{Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:]), Format: "zstd"}
	snapshot, err := SnapshotBlob(object, blob, t.TempDir(), "archive")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(object, []byte("replaced source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(snapshot)
	if err != nil || string(actual) != string(content) {
		t.Fatalf("authenticated snapshot changed with source: %q, %v", actual, err)
	}
}
