package device

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Info struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Size       int64  `json:"size"`
	MajorMinor string `json:"major_minor"`
	StableID   string `json:"stable_id"`
}

func Inspect(path string, minimumSize int64, allowLoop bool) (Info, error) {
	if !filepath.IsAbs(path) || filepath.Dir(path) != "/dev" || filepath.Clean(path) != path {
		return Info{}, errors.New("target must be an explicit top-level /dev path")
	}
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return Info{}, fmt.Errorf("inspect target device: %w", err)
	}
	if fileInfo.Mode()&os.ModeSymlink != 0 || fileInfo.Mode()&os.ModeDevice == 0 || fileInfo.Mode()&os.ModeCharDevice != 0 {
		return Info{}, errors.New("target is not a non-symlink block device")
	}
	kind, err := run("lsblk", "--noheadings", "--nodeps", "--output", "TYPE", "--", path)
	if err != nil {
		return Info{}, err
	}
	kind = strings.TrimSpace(kind)
	if kind != "disk" && !(allowLoop && kind == "loop") {
		return Info{}, fmt.Errorf("target has unsupported block-device type %q", kind)
	}
	sizeText, err := run("blockdev", "--getsize64", path)
	if err != nil {
		return Info{}, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeText), 10, 64)
	if err != nil || size < minimumSize {
		return Info{}, fmt.Errorf("target device is smaller than the selected release (%d bytes)", minimumSize)
	}
	mounts, err := run("lsblk", "--noheadings", "--raw", "--paths", "--output", "MOUNTPOINTS", "--", path)
	if err != nil {
		return Info{}, err
	}
	if strings.TrimSpace(mounts) != "" {
		return Info{}, errors.New("target device or one of its partitions is mounted")
	}
	if err := rejectRunningRoot(path); err != nil {
		return Info{}, err
	}
	majorMinor, err := run("lsblk", "--noheadings", "--nodeps", "--output", "MAJ:MIN", "--", path)
	if err != nil {
		return Info{}, err
	}
	majorMinor = strings.TrimSpace(majorMinor)
	if majorMinor == "" || strings.ContainsAny(majorMinor, " \t\n") {
		return Info{}, errors.New("could not resolve target device identity")
	}
	stableHash := sha256.Sum256([]byte(kind + "\x00" + strconv.FormatInt(size, 10) + "\x00" + majorMinor))
	return Info{
		Path: path, Kind: kind, Size: size, MajorMinor: majorMinor,
		StableID: "sha256:" + hex.EncodeToString(stableHash[:]),
	}, nil
}

func rejectRunningRoot(target string) error {
	rootSource, err := run("findmnt", "--noheadings", "--raw", "--output", "SOURCE", "/")
	if err != nil {
		return err
	}
	rootSource = strings.TrimSpace(rootSource)
	if !strings.HasPrefix(rootSource, "/dev/") {
		return nil
	}
	rootInfo, err := os.Stat(rootSource)
	if err != nil {
		return fmt.Errorf("inspect running root source: %w", err)
	}
	names, err := run("lsblk", "--noheadings", "--raw", "--paths", "--output", "NAME", "--", target)
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(names) {
		candidate, statErr := os.Stat(name)
		if statErr == nil && os.SameFile(rootInfo, candidate) {
			return errors.New("refusing to overwrite the disk containing the running root filesystem")
		}
	}
	return nil
}

func run(name string, arguments ...string) (string, error) {
	command := exec.Command(name, arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
