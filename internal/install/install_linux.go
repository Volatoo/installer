package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Volatoo/installer/internal/device"
	"github.com/Volatoo/installer/internal/release"
)

var (
	stateStartPattern = regexp.MustCompile(`(?m)^First sector: ([1-9][0-9]*)`)
	stateGUIDPattern  = regexp.MustCompile(`(?m)^Partition unique GUID: ([0-9A-Fa-f-]{36})`)
)

type Options struct {
	ArchivePath      string
	Device           device.Info
	Selection        release.Selection
	SigningKeyID     string
	SigningKeyHash   string
	SSHKeyPath       string
	ProvisionAccess  bool
	InstallerVersion string
	Now              time.Time
}

type Receipt struct {
	Schema           string `json:"schema"`
	ReleaseID        string `json:"release_id"`
	Channel          string `json:"channel"`
	IndexSequence    uint64 `json:"index_sequence"`
	IndexSHA256      string `json:"index_sha256"`
	SigningKeyID     string `json:"signing_key_id"`
	SigningKeySHA256 string `json:"signing_key_sha256"`
	ArchiveSHA256    string `json:"archive_sha256"`
	ManifestSHA256   string `json:"manifest_sha256"`
	DiskSHA256       string `json:"disk_sha256"`
	TargetStableID   string `json:"target_stable_id"`
	TargetMajorMinor string `json:"target_major_minor"`
	InstalledAt      string `json:"installed_at"`
	InstallerVersion string `json:"installer_version"`
}

func Apply(options Options) error {
	if os.Geteuid() != 0 {
		return errors.New("installation requires root")
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	var authorizedKeys []byte
	if options.ProvisionAccess {
		var err error
		authorizedKeys, err = loadSSHKeys(options.SSHKeyPath)
		if err != nil {
			return err
		}
	}
	if err := writeDisk(options.ArchivePath, options.Device, options.Selection.Target.Disk.Size, options.Selection.Target.Disk.SHA256); err != nil {
		return err
	}
	if err := rereadPartitions(options.Device.Path); err != nil {
		return err
	}
	statePartition := partitionPath(options.Device.Path, 4)
	if options.Device.Size > options.Selection.Target.Disk.Size {
		if err := expandState(options.Device.Path, statePartition); err != nil {
			return err
		}
	}
	if err := waitForPartition(statePartition); err != nil {
		return err
	}
	receipt := Receipt{
		Schema: "org.volatoo.install-receipt/v1", ReleaseID: options.Selection.Target.ID,
		Channel: options.Selection.Index.Channel, IndexSequence: options.Selection.Index.Sequence,
		IndexSHA256: options.Selection.IndexDigest, SigningKeyID: options.SigningKeyID,
		SigningKeySHA256: options.SigningKeyHash, ArchiveSHA256: options.Selection.Target.Archive.SHA256,
		ManifestSHA256: options.Selection.Target.Manifest.SHA256, DiskSHA256: options.Selection.Target.Disk.SHA256,
		TargetStableID: options.Device.StableID, TargetMajorMinor: options.Device.MajorMinor,
		InstalledAt: options.Now.UTC().Format(time.RFC3339), InstallerVersion: options.InstallerVersion,
	}
	if err := writeState(statePartition, receipt, authorizedKeys, options.ProvisionAccess); err != nil {
		return err
	}
	if _, err := run("sync"); err != nil {
		return err
	}
	return nil
}

func writeDisk(archivePath string, targetInfo device.Info, expectedSize int64, expectedSHA256 string) error {
	command := exec.Command("zstd", "--decompress", "--stdout", "--no-progress", "--", archivePath)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("prepare disk decompression: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	target, err := openVerifiedBlockDevice(targetInfo, syscall.O_WRONLY|syscall.O_SYNC)
	if err != nil {
		return fmt.Errorf("open explicit target device: %w", err)
	}
	defer target.Close()
	if err := command.Start(); err != nil {
		return fmt.Errorf("start disk decompression: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.CopyN(io.MultiWriter(target, hash), stdout, expectedSize+1)
	waitErr := command.Wait()
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		return fmt.Errorf("write target device: %w", copyErr)
	}
	if waitErr != nil {
		return fmt.Errorf("decompress release archive: %w: %s", waitErr, bytes.TrimSpace(stderr.Bytes()))
	}
	if written != expectedSize {
		return fmt.Errorf("decompressed disk size differs: got %d, want %d", written, expectedSize)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
		return errors.New("decompressed disk digest changed after preflight verification")
	}
	if err := target.Sync(); err != nil {
		return fmt.Errorf("sync target device: %w", err)
	}
	if err := target.Close(); err != nil {
		return fmt.Errorf("close target device: %w", err)
	}
	return verifyReadback(targetInfo, expectedSize, expectedSHA256)
}

func verifyReadback(targetInfo device.Info, expectedSize int64, expectedSHA256 string) error {
	file, err := openVerifiedBlockDevice(targetInfo, syscall.O_RDONLY)
	if err != nil {
		return fmt.Errorf("open installed disk for readback: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.CopyN(hash, file, expectedSize)
	if err != nil || written != expectedSize {
		return fmt.Errorf("read back installed disk: read %d of %d bytes: %w", written, expectedSize, err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
		return errors.New("installed disk readback SHA-256 differs")
	}
	return nil
}

func openVerifiedBlockDevice(targetInfo device.Info, flags int) (*os.File, error) {
	descriptor, err := syscall.Open(targetInfo.Path, flags|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open explicit target device without following symlinks: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), targetInfo.Path)
	var stat syscall.Stat_t
	if err := syscall.Fstat(descriptor, &stat); err != nil {
		file.Close()
		return nil, fmt.Errorf("inspect opened target device: %w", err)
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFBLK || formatDeviceNumber(uint64(stat.Rdev)) != targetInfo.MajorMinor {
		file.Close()
		return nil, errors.New("opened target device identity differs from the confirmed plan")
	}
	return file, nil
}

func formatDeviceNumber(rdev uint64) string {
	major := ((rdev >> 8) & 0xfff) | ((rdev >> 32) & 0xfffff000)
	minor := (rdev & 0xff) | ((rdev >> 12) & 0xffffff00)
	return fmt.Sprintf("%d:%d", major, minor)
}

func expandState(devicePath, statePartition string) error {
	stateInfo, err := run("sgdisk", "--info=4", devicePath)
	if err != nil {
		return err
	}
	startMatch := stateStartPattern.FindStringSubmatch(stateInfo)
	guidMatch := stateGUIDPattern.FindStringSubmatch(stateInfo)
	if len(startMatch) != 2 || len(guidMatch) != 2 {
		return errors.New("could not read state partition identity")
	}
	if _, err := strconv.ParseUint(startMatch[1], 10, 64); err != nil {
		return errors.New("state partition has invalid start sector")
	}
	if _, err := run("sgdisk", "--move-second-header", devicePath); err != nil {
		return err
	}
	if _, err := run("sgdisk", "--delete=4", "--new=4:"+startMatch[1]+":0", "--typecode=4:8300", "--change-name=4:VOLATOO-STATE", "--partition-guid=4:"+guidMatch[1], devicePath); err != nil {
		return err
	}
	if err := rereadPartitions(devicePath); err != nil {
		return err
	}
	if err := waitForPartition(statePartition); err != nil {
		return err
	}
	_, checkErr := run("e2fsck", "-fy", statePartition)
	if checkErr != nil {
		var exitError *commandExitError
		if !errors.As(checkErr, &exitError) || exitError.Code > 1 {
			return checkErr
		}
	}
	if _, err := run("resize2fs", statePartition); err != nil {
		return err
	}
	return nil
}

func rereadPartitions(devicePath string) error {
	_, rereadErr := run("blockdev", "--rereadpt", devicePath)
	_, addErr := run("partx", "--add", devicePath)
	_, updateErr := run("partx", "--update", devicePath)
	if rereadErr != nil && addErr != nil && updateErr != nil {
		return fmt.Errorf("kernel refused to reload target partitions: %v; %v; %w", rereadErr, addErr, updateErr)
	}
	if _, err := exec.LookPath("udevadm"); err == nil {
		if _, err := run("udevadm", "settle"); err != nil {
			return err
		}
	} else if _, err := exec.LookPath("mdev"); err == nil {
		if _, err := run("mdev", "-s"); err != nil {
			return err
		}
	}
	return nil
}

func waitForPartition(path string) error {
	for attempt := 0; attempt < 50; attempt++ {
		matches, present := partitionNodeMatchesSysfs(path)
		if matches {
			if _, err := run("blockdev", "--getsize64", path); err == nil {
				return nil
			}
		}
		if attempt%10 == 0 {
			if _, err := exec.LookPath("mdev"); err == nil {
				if present && !matches && filepath.Dir(path) == "/dev" {
					_ = os.Remove(path)
				}
				_, _ = run("mdev", "-s")
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("state partition did not appear: %s", path)
}

func partitionNodeMatchesSysfs(path string) (matches bool, sysfsPresent bool) {
	expected, err := os.ReadFile(filepath.Join("/sys/class/block", filepath.Base(path), "dev"))
	if err != nil {
		return false, false
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
		return false, true
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, true
	}
	actual := formatDeviceNumber(uint64(stat.Rdev))
	return actual == strings.TrimSpace(string(expected)), true
}

func writeState(partition string, receipt Receipt, authorizedKeys []byte, provisionAccess bool) error {
	mountPath, err := os.MkdirTemp("", "volatoo-state.")
	if err != nil {
		return fmt.Errorf("create state mountpoint: %w", err)
	}
	mounted := false
	defer func() {
		if mounted {
			_, _ = run("umount", mountPath)
		}
		_ = os.Remove(mountPath)
	}()
	if _, err := run("mount", "--options", "rw", "--", partition, mountPath); err != nil {
		return err
	}
	mounted = true
	configPath := filepath.Join(mountPath, "volatoo", "config")
	if err := requireSafeDirectory(configPath); err != nil {
		return errors.New("destination has no safe Volatoo state configuration directory")
	}
	if provisionAccess {
		accessPath := filepath.Join(configPath, "access")
		if err := ensureSafeDirectory(accessPath, 0o700); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(accessPath, "authorized_keys"), authorizedKeys, 0o600); err != nil {
			return err
		}
	}
	receiptPath := filepath.Join(mountPath, "volatoo", "install")
	if err := ensureSafeDirectory(receiptPath, 0o700); err != nil {
		return err
	}
	receiptDocument, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("encode installation receipt: %w", err)
	}
	receiptDocument = append(receiptDocument, '\n')
	if err := writeAtomic(filepath.Join(receiptPath, "receipt-v1.json"), receiptDocument, 0o600); err != nil {
		return err
	}
	if _, err := run("sync", "--file-system", mountPath); err != nil {
		return err
	}
	if _, err := run("umount", mountPath); err != nil {
		return err
	}
	mounted = false
	return os.Remove(mountPath)
}

func writeAtomic(destination string, content []byte, mode os.FileMode) error {
	if info, err := os.Lstat(destination); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing unsafe symlink destination: %s", destination)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect destination: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".new.*")
	if err != nil {
		return fmt.Errorf("create atomic state file: %w", err)
	}
	temporary := file.Name()
	clean := false
	defer func() {
		_ = file.Close()
		if !clean {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("set atomic state file mode: %w", err)
	}
	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("write atomic state file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync atomic state file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close atomic state file: %w", err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("publish atomic state file: %w", err)
	}
	clean = true
	return nil
}

func requireSafeDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe directory")
	}
	return nil
}

func ensureSafeDirectory(path string, mode os.FileMode) error {
	if err := os.Mkdir(path, mode); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create state directory: %w", err)
	}
	if err := requireSafeDirectory(path); err != nil {
		return fmt.Errorf("unsafe state directory: %s", path)
	}
	return os.Chmod(path, mode)
}

func loadSSHKeys(path string) ([]byte, error) {
	descriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open administrator SSH key without following symlinks: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), path)
	defer file.Close()
	var stat syscall.Stat_t
	if err := syscall.Fstat(descriptor, &stat); err != nil {
		return nil, fmt.Errorf("inspect opened administrator SSH key: %w", err)
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || stat.Size <= 0 || stat.Size > 1<<20 {
		return nil, errors.New("administrator SSH key must be a non-empty regular file no larger than 1 MiB")
	}
	content, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read administrator SSH key: %w", err)
	}
	if len(content) == 0 || len(content) > 1<<20 {
		return nil, errors.New("administrator SSH key changed or exceeds 1 MiB while reading")
	}
	validTypes := map[string]bool{
		"ssh-ed25519": true, "sk-ssh-ed25519@openssh.com": true, "ssh-rsa": true,
		"ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true, "ecdsa-sha2-nistp521": true,
		"sk-ecdsa-sha2-nistp256@openssh.com": true,
	}
	keys := 0
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if len(fields) < 2 || !validTypes[fields[0]] {
			return nil, errors.New("administrator SSH public key file is invalid")
		}
		if _, err := base64.StdEncoding.Strict().DecodeString(fields[1]); err != nil {
			if _, rawErr := base64.RawStdEncoding.Strict().DecodeString(fields[1]); rawErr != nil {
				return nil, errors.New("administrator SSH public key file is invalid")
			}
		}
		keys++
	}
	if keys == 0 {
		return nil, errors.New("administrator SSH public key file contains no keys")
	}
	return content, nil
}

func partitionPath(devicePath string, number int) string {
	if last := devicePath[len(devicePath)-1]; last >= '0' && last <= '9' {
		return fmt.Sprintf("%sp%d", devicePath, number)
	}
	return fmt.Sprintf("%s%d", devicePath, number)
}

type commandExitError struct {
	Name string
	Code int
	Err  error
}

func (errorValue *commandExitError) Error() string {
	return fmt.Sprintf("%s failed with status %d: %v", errorValue.Name, errorValue.Code, errorValue.Err)
}
func (errorValue *commandExitError) Unwrap() error { return errorValue.Err }

func run(name string, arguments ...string) (string, error) {
	command := exec.Command(name, arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return "", &commandExitError{Name: name, Code: exitError.ExitCode(), Err: fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))}
		}
		return "", fmt.Errorf("%s failed: %w", name, err)
	}
	return stdout.String(), nil
}
