package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

func VerifyFile(path string, expectedSize int64, expectedSHA256 string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("artifact is not a regular non-symlink file: %s", path)
	}
	if info.Size() != expectedSize {
		return fmt.Errorf("artifact size differs for %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
		return fmt.Errorf("artifact SHA-256 differs for %s", path)
	}
	return nil
}

func VerifyZstdDisk(archivePath string, expectedSize int64, expectedSHA256 string) error {
	command := exec.Command("zstd", "--decompress", "--stdout", "--no-progress", "--", archivePath)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("prepare zstd verifier: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start zstd verifier: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.CopyN(hash, stdout, expectedSize+1)
	waitErr := command.Wait()
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		return fmt.Errorf("decompress release archive: %w", copyErr)
	}
	if waitErr != nil {
		return fmt.Errorf("decompress release archive: %w: %s", waitErr, bytes.TrimSpace(stderr.Bytes()))
	}
	if written != expectedSize {
		return fmt.Errorf("uncompressed disk size differs: got %d, want %d", written, expectedSize)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
		return errors.New("uncompressed disk SHA-256 differs")
	}
	return nil
}
