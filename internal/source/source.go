package source

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Volatoo/installer/internal/release"
)

var client = &http.Client{
	Timeout: 30 * time.Minute,
	CheckRedirect: func(request *http.Request, previous []*http.Request) error {
		if len(previous) >= 5 {
			return errors.New("too many HTTP redirects")
		}
		if request.URL.Scheme != "https" || request.URL.User != nil || request.URL.Fragment != "" {
			return errors.New("release redirect is not a credential-free HTTPS URL")
		}
		return nil
	},
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DisableCompression:  true,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 30 * time.Second,
	},
}

func Read(location string, maximum int64) ([]byte, error) {
	parsed, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("parse source %q: %w", location, err)
	}
	if parsed.IsAbs() {
		if err := validateHTTPS(parsed); err != nil {
			return nil, err
		}
		response, err := client.Get(parsed.String())
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", parsed.Redacted(), err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("download %s: HTTP %s", parsed.Redacted(), response.Status)
		}
		if response.ContentLength > maximum {
			return nil, fmt.Errorf("download exceeds size limit: %s", parsed.Redacted())
		}
		content, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", parsed.Redacted(), err)
		}
		if int64(len(content)) == 0 || int64(len(content)) > maximum {
			return nil, fmt.Errorf("download has unsafe size: %s", parsed.Redacted())
		}
		return content, nil
	}
	return readLocal(location, maximum)
}

func FetchBlob(indexLocation string, blob release.Blob, directory, name string) (string, error) {
	location, err := Resolve(indexLocation, blob.URL)
	if err != nil {
		return "", err
	}
	return SnapshotBlob(location, blob, directory, name)
}

// SnapshotBlob copies one authenticated blob into a private installer
// workspace. Callers must use the returned path after verification so an
// operator-supplied offline path cannot be replaced while confirmation is
// pending.
func SnapshotBlob(location string, blob release.Blob, directory, name string) (string, error) {
	destination := filepath.Join(directory, name)
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create artifact destination: %w", err)
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(destination)
		}
	}()
	hash := sha256.New()
	written, err := copyLocation(io.MultiWriter(file, hash), location, blob.Size)
	if err != nil {
		return "", err
	}
	if written != blob.Size {
		return "", fmt.Errorf("artifact size differs for %s", redact(location))
	}
	if hex.EncodeToString(hash.Sum(nil)) != blob.SHA256 {
		return "", fmt.Errorf("artifact SHA-256 differs for %s", redact(location))
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync downloaded artifact: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close downloaded artifact: %w", err)
	}
	complete = true
	return destination, nil
}

func Resolve(indexLocation, artifactLocation string) (string, error) {
	artifact, err := url.Parse(artifactLocation)
	if err != nil {
		return "", fmt.Errorf("parse artifact URL: %w", err)
	}
	if artifact.IsAbs() {
		if err := validateHTTPS(artifact); err != nil {
			return "", err
		}
		return artifact.String(), nil
	}
	indexURL, err := url.Parse(indexLocation)
	if err != nil {
		return "", fmt.Errorf("parse release-index source: %w", err)
	}
	if indexURL.IsAbs() {
		if err := validateHTTPS(indexURL); err != nil {
			return "", err
		}
		resolved := indexURL.ResolveReference(artifact)
		if err := validateHTTPS(resolved); err != nil {
			return "", err
		}
		return resolved.String(), nil
	}
	if filepath.IsAbs(artifactLocation) || strings.Contains(artifactLocation, `\`) {
		return "", errors.New("invalid relative local artifact path")
	}
	base, err := filepath.Abs(filepath.Dir(indexLocation))
	if err != nil {
		return "", fmt.Errorf("resolve local release-index directory: %w", err)
	}
	resolved, err := filepath.Abs(filepath.Join(base, filepath.FromSlash(artifactLocation)))
	if err != nil {
		return "", fmt.Errorf("resolve local artifact: %w", err)
	}
	return resolved, nil
}

func copyLocation(destination io.Writer, location string, expectedSize int64) (int64, error) {
	parsed, _ := url.Parse(location)
	var reader io.ReadCloser
	if parsed.IsAbs() {
		response, err := client.Get(parsed.String())
		if err != nil {
			return 0, fmt.Errorf("download %s: %w", parsed.Redacted(), err)
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return 0, fmt.Errorf("download %s: HTTP %s", parsed.Redacted(), response.Status)
		}
		if response.ContentLength >= 0 && response.ContentLength != expectedSize {
			response.Body.Close()
			return 0, fmt.Errorf("artifact Content-Length differs for %s", parsed.Redacted())
		}
		reader = response.Body
	} else {
		info, err := os.Lstat(location)
		if err != nil {
			return 0, fmt.Errorf("inspect local artifact: %w", err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != expectedSize {
			return 0, errors.New("local artifact is not a matching regular non-symlink file")
		}
		file, err := os.Open(location)
		if err != nil {
			return 0, fmt.Errorf("open local artifact: %w", err)
		}
		openedInfo, err := file.Stat()
		if err != nil {
			file.Close()
			return 0, fmt.Errorf("inspect opened local artifact: %w", err)
		}
		if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() || openedInfo.Size() != expectedSize {
			file.Close()
			return 0, errors.New("local artifact changed while opening")
		}
		reader = file
	}
	defer reader.Close()
	written, err := io.CopyN(destination, reader, expectedSize+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return written, fmt.Errorf("read artifact %s: %w", redact(location), err)
	}
	return written, nil
}

func readLocal(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("input is not a regular non-symlink file: %s", path)
	}
	if info.Size() <= 0 || info.Size() > maximum {
		return nil, fmt.Errorf("input size is unsafe for %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return content, nil
}

func validateHTTPS(parsed *url.URL) error {
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("published sources must be credential-free HTTPS URLs")
	}
	return nil
}

func redact(location string) string {
	parsed, err := url.Parse(location)
	if err == nil && parsed.IsAbs() {
		return parsed.Redacted()
	}
	return location
}
