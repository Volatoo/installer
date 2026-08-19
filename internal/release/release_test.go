package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

func validIndex() Index {
	digest := strings.Repeat("a", 64)
	return Index{
		Schema: SchemaV1, Channel: "v0.1-dev", Sequence: 7,
		PublishedAt: testNow.Add(-time.Hour), ExpiresAt: testNow.Add(time.Hour),
		Releases: []Target{{
			ID: "v0.1.0-dev.20260815-openrc-amd64", Architecture: "amd64", InitSystem: "openrc",
			Archive:  Blob{URL: "objects/sha256/aa/archive", Size: 123, SHA256: digest, Format: "zstd"},
			Manifest: Blob{URL: "https://dist.volatoo.org/objects/sha256/aa/manifest", Size: 456, SHA256: digest, Format: "release-media-v2"},
			Disk:     Disk{File: "volatoo-openrc-amd64.img", Size: 789, SHA256: digest, Format: "raw-gpt"},
		}},
	}
}

func marshalIndex(t *testing.T, index Index) []byte {
	t.Helper()
	document, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestParseAndSelect(t *testing.T) {
	selection, err := ParseAndSelect(marshalIndex(t, validIndex()), testNow, "v0.1-dev", "amd64", "openrc")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Target.ID != "v0.1.0-dev.20260815-openrc-amd64" || len(selection.IndexDigest) != 64 {
		t.Fatalf("unexpected selection: %#v", selection)
	}
}

func TestParseAndSelectRejectsUnsafeDocuments(t *testing.T) {
	tests := map[string]func(Index) Index{
		"expired":       func(index Index) Index { index.ExpiresAt = testNow; return index },
		"not-yet-valid": func(index Index) Index { index.PublishedAt = testNow.Add(time.Second); return index },
		"zero-sequence": func(index Index) Index { index.Sequence = 0; return index },
		"duplicate-target": func(index Index) Index {
			index.Releases = append(index.Releases, index.Releases[0])
			index.Releases[1].ID += "-copy"
			return index
		},
		"plain-http": func(index Index) Index {
			index.Releases[0].Archive.URL = "http://dist.volatoo.org/archive"
			return index
		},
		"wrong-channel": func(index Index) Index { index.Channel = "stable"; return index },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAndSelect(marshalIndex(t, mutate(validIndex())), testNow, "v0.1-dev", "amd64", "openrc"); err == nil {
				t.Fatal("unsafe index was accepted")
			}
		})
	}
	unknown := strings.TrimSuffix(string(marshalIndex(t, validIndex())), "}") + `,"future_meaning":true}`
	if _, err := ParseAndSelect([]byte(unknown), testNow, "v0.1-dev", "amd64", "openrc"); err == nil {
		t.Fatal("unknown field was accepted")
	}
	duplicate := strings.Replace(string(marshalIndex(t, validIndex())), `"channel":"v0.1-dev"`, `"channel":"v0.1-dev","channel":"stable"`, 1)
	if _, err := ParseAndSelect([]byte(duplicate), testNow, "v0.1-dev", "amd64", "openrc"); err == nil {
		t.Fatal("duplicate JSON object name was accepted")
	}
}

func TestVerifySignify(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyID := []byte("release1")
	publicFile := signifyFile("untrusted comment: test key", append(append([]byte("Ed"), keyID...), public...))
	document := []byte("signed release index\n")
	signatureFile := signifyFile("untrusted comment: test signature", append(append([]byte("Ed"), keyID...), ed25519.Sign(private, document)...))
	verified, err := VerifySignify(document, signatureFile, [][]byte{publicFile})
	if err != nil {
		t.Fatal(err)
	}
	if verified.ID != "72656c6561736531" || len(verified.FileSHA256) != 64 {
		t.Fatalf("unexpected verified key: %#v", verified)
	}
	if _, err := VerifySignify(append(document, 'x'), signatureFile, [][]byte{publicFile}); err == nil {
		t.Fatal("changed document was accepted")
	}
	if _, err := VerifySignify(document, signatureFile, [][]byte{publicFile, publicFile}); err == nil {
		t.Fatal("duplicate trusted key ID was accepted")
	}
}

func signifyFile(comment string, payload []byte) []byte {
	return []byte(comment + "\n" + base64.StdEncoding.EncodeToString(payload) + "\n")
}
