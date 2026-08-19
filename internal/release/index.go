package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const SchemaV1 = "org.volatoo.release-index/v1"

var (
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

type Index struct {
	Schema      string    `json:"schema"`
	Channel     string    `json:"channel"`
	Sequence    uint64    `json:"sequence"`
	PublishedAt time.Time `json:"published_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Releases    []Target  `json:"releases"`
}

type Target struct {
	ID           string `json:"id"`
	Architecture string `json:"architecture"`
	InitSystem   string `json:"init_system"`
	Archive      Blob   `json:"archive"`
	Manifest     Blob   `json:"manifest"`
	Disk         Disk   `json:"disk"`
}

type Blob struct {
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Format string `json:"format"`
}

type Disk struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Format string `json:"format"`
}

type Selection struct {
	IndexDigest string
	Index       Index
	Target      Target
}

func ParseAndSelect(document []byte, now time.Time, channel, architecture, initSystem string) (Selection, error) {
	if err := rejectDuplicateJSONNames(document); err != nil {
		return Selection{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var index Index
	if err := decoder.Decode(&index); err != nil {
		return Selection{}, fmt.Errorf("parse release index: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Selection{}, err
	}
	if err := validateIndex(index, now); err != nil {
		return Selection{}, err
	}
	if index.Channel != channel {
		return Selection{}, fmt.Errorf("release index targets channel %q, not %q", index.Channel, channel)
	}
	var matches []Target
	for _, target := range index.Releases {
		if target.Architecture == architecture && target.InitSystem == initSystem {
			matches = append(matches, target)
		}
	}
	if len(matches) != 1 {
		return Selection{}, fmt.Errorf("release index contains %d targets for %s/%s; require exactly one", len(matches), architecture, initSystem)
	}
	digest := sha256.Sum256(document)
	return Selection{IndexDigest: hex.EncodeToString(digest[:]), Index: index, Target: matches[0]}, nil
}

func rejectDuplicateJSONNames(document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	if err := scanJSONValue(decoder); err != nil {
		return fmt.Errorf("parse release index: %w", err)
	}
	return requireJSONEOF(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("JSON object name is not a string")
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("duplicate JSON object name %q", name)
			}
			seen[name] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("release index has an unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("release index has an unterminated JSON array")
		}
	default:
		return errors.New("release index contains an unexpected JSON delimiter")
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("parse trailing release-index data: %w", err)
	}
	return errors.New("release index contains more than one JSON value")
}

func validateIndex(index Index, now time.Time) error {
	if index.Schema != SchemaV1 {
		return fmt.Errorf("unsupported release-index schema %q", index.Schema)
	}
	if !idPattern.MatchString(index.Channel) {
		return errors.New("release index has invalid channel")
	}
	if index.Sequence == 0 {
		return errors.New("release index sequence must be positive")
	}
	if index.PublishedAt.IsZero() || index.ExpiresAt.IsZero() || !index.PublishedAt.Before(index.ExpiresAt) {
		return errors.New("release index has an invalid validity window")
	}
	if now.Before(index.PublishedAt) {
		return errors.New("release index is not valid yet")
	}
	if !now.Before(index.ExpiresAt) {
		return errors.New("release index has expired")
	}
	if len(index.Releases) == 0 {
		return errors.New("release index contains no targets")
	}
	seenIDs := make(map[string]struct{}, len(index.Releases))
	seenVariants := make(map[string]struct{}, len(index.Releases))
	for position, target := range index.Releases {
		if err := validateTarget(target); err != nil {
			return fmt.Errorf("release target %d: %w", position, err)
		}
		if _, exists := seenIDs[target.ID]; exists {
			return fmt.Errorf("duplicate release ID %q", target.ID)
		}
		seenIDs[target.ID] = struct{}{}
		variant := target.Architecture + "\x00" + target.InitSystem
		if _, exists := seenVariants[variant]; exists {
			return fmt.Errorf("duplicate release target %s/%s", target.Architecture, target.InitSystem)
		}
		seenVariants[variant] = struct{}{}
	}
	return nil
}

func validateTarget(target Target) error {
	if !idPattern.MatchString(target.ID) {
		return errors.New("invalid release ID")
	}
	if target.Architecture != "amd64" {
		return fmt.Errorf("unsupported architecture %q", target.Architecture)
	}
	if target.InitSystem != "openrc" && target.InitSystem != "systemd" {
		return fmt.Errorf("unsupported init system %q", target.InitSystem)
	}
	if err := validateBlob("archive", target.Archive, "zstd"); err != nil {
		return err
	}
	if err := validateBlob("manifest", target.Manifest, "release-media-v2"); err != nil {
		return err
	}
	if !idPattern.MatchString(target.Disk.File) || !strings.HasSuffix(target.Disk.File, ".img") {
		return errors.New("invalid disk filename")
	}
	if target.Disk.Size <= 0 || !digestPattern.MatchString(target.Disk.SHA256) || target.Disk.Format != "raw-gpt" {
		return errors.New("invalid disk size or SHA-256")
	}
	return nil
}

func validateBlob(description string, blob Blob, expectedFormat string) error {
	if blob.Size <= 0 || !digestPattern.MatchString(blob.SHA256) || blob.Format != expectedFormat {
		return fmt.Errorf("invalid %s size or SHA-256", description)
	}
	parsed, err := url.Parse(blob.URL)
	if err != nil || parsed.String() != blob.URL || parsed.Fragment != "" || parsed.User != nil {
		return fmt.Errorf("invalid %s URL", description)
	}
	if parsed.IsAbs() {
		if parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("%s URL must use HTTPS", description)
		}
	} else if blob.URL == "" || strings.HasPrefix(blob.URL, "/") || strings.Contains(blob.URL, `\`) {
		return fmt.Errorf("invalid relative %s URL", description)
	}
	return nil
}
