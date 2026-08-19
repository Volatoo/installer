package media

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Volatoo/installer/internal/release"
)

const SchemaV2 = "org.volatoo.release-media/v2"

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Manifest struct {
	Schema               string
	Channel              string
	InitSystem           string
	DiskFile             string
	DiskSize             int64
	DiskSHA256           string
	KernelSHA256         string
	InitramfsSHA256      string
	RootfsSHA256         string
	StateSHA256          string
	SecureBoot           bool
	SecureBootCertSHA256 string
	UKISHA256            string
}

func ParseAndMatch(document []byte, selection release.Selection) (Manifest, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(document))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			return Manifest{}, errors.New("release-media manifest contains an empty line")
		}
		key, value, found := strings.Cut(line, "=")
		if !found || key == "" || value == "" {
			return Manifest{}, errors.New("release-media manifest contains an invalid record")
		}
		if _, exists := values[key]; exists {
			return Manifest{}, fmt.Errorf("release-media manifest contains duplicate %s", key)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return Manifest{}, fmt.Errorf("read release-media manifest: %w", err)
	}
	required := []string{
		"schema", "channel", "init_system", "disk_file", "disk_size", "disk_sha256",
		"kernel_sha256", "initramfs_sha256", "rootfs_sha256", "state_sha256",
		"secure_boot", "secure_boot_cert_sha256", "uki_sha256",
	}
	if len(values) != len(required) {
		return Manifest{}, errors.New("release-media manifest has missing or unknown fields")
	}
	for _, key := range required {
		if _, exists := values[key]; !exists {
			return Manifest{}, fmt.Errorf("release-media manifest is missing %s", key)
		}
	}
	diskSize, err := strconv.ParseInt(values["disk_size"], 10, 64)
	if err != nil || diskSize <= 0 {
		return Manifest{}, errors.New("release-media manifest has invalid disk_size")
	}
	manifest := Manifest{
		Schema:               values["schema"],
		Channel:              values["channel"],
		InitSystem:           values["init_system"],
		DiskFile:             values["disk_file"],
		DiskSize:             diskSize,
		DiskSHA256:           values["disk_sha256"],
		KernelSHA256:         values["kernel_sha256"],
		InitramfsSHA256:      values["initramfs_sha256"],
		RootfsSHA256:         values["rootfs_sha256"],
		StateSHA256:          values["state_sha256"],
		SecureBootCertSHA256: values["secure_boot_cert_sha256"],
		UKISHA256:            values["uki_sha256"],
	}
	if manifest.Schema != SchemaV2 {
		return Manifest{}, fmt.Errorf("unsupported release-media schema %q", manifest.Schema)
	}
	for name, digest := range map[string]string{
		"disk_sha256": manifest.DiskSHA256, "kernel_sha256": manifest.KernelSHA256,
		"initramfs_sha256": manifest.InitramfsSHA256, "rootfs_sha256": manifest.RootfsSHA256,
		"state_sha256": manifest.StateSHA256,
	} {
		if !digestPattern.MatchString(digest) {
			return Manifest{}, fmt.Errorf("release-media manifest has invalid %s", name)
		}
	}
	switch values["secure_boot"] {
	case "yes":
		manifest.SecureBoot = true
		if !digestPattern.MatchString(manifest.SecureBootCertSHA256) || !digestPattern.MatchString(manifest.UKISHA256) {
			return Manifest{}, errors.New("signed release-media manifest has invalid Secure Boot provenance")
		}
	case "no":
		if manifest.SecureBootCertSHA256 != "none" || manifest.UKISHA256 != "none" {
			return Manifest{}, errors.New("unsigned release-media manifest claims Secure Boot provenance")
		}
	default:
		return Manifest{}, errors.New("release-media manifest has invalid secure_boot")
	}
	target := selection.Target
	if manifest.Channel != selection.Index.Channel || manifest.InitSystem != target.InitSystem ||
		manifest.DiskFile != target.Disk.File || manifest.DiskSize != target.Disk.Size ||
		manifest.DiskSHA256 != target.Disk.SHA256 {
		return Manifest{}, errors.New("release-media manifest identity differs from signed release index")
	}
	return manifest, nil
}
