package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Volatoo/installer/internal/artifact"
	"github.com/Volatoo/installer/internal/device"
	installer "github.com/Volatoo/installer/internal/install"
	"github.com/Volatoo/installer/internal/media"
	"github.com/Volatoo/installer/internal/release"
	"github.com/Volatoo/installer/internal/source"
)

var version = "0.1.0-dev"

const defaultTrustedKeyDirectory = "/usr/share/volatoo/keyring/release"

var trustedKeyName = regexp.MustCompile(`^[0-9a-f]{64}\.pub$`)

type stringList []string

func (values *stringList) String() string { return fmt.Sprint([]string(*values)) }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("usage: volatoo-installer verify|install [OPTIONS]")
	}
	switch arguments[0] {
	case "version", "--version":
		fmt.Println(version)
		return nil
	case "verify":
		return runVerify(arguments[1:])
	case "install":
		return runInstall(arguments[1:])
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func runVerify(arguments []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	options, keys := addBundleFlags(flags)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("verify accepts no positional arguments")
	}
	bundle, err := verifyBundle(options, keys)
	if err != nil {
		return err
	}
	defer bundle.Cleanup()
	fmt.Printf("verified %s (%s/%s) with release key %s\n", bundle.Selection.Target.ID, bundle.Selection.Target.Architecture, bundle.Selection.Target.InitSystem, bundle.Key.ID)
	return nil
}

type bundleFlags struct {
	IndexPath     *string
	SignaturePath *string
	Channel       *string
	Architecture  *string
	InitSystem    *string
	ArchivePath   *string
	ManifestPath  *string
	TrustedKeyDir *string
}

type verifiedBundle struct {
	Selection release.Selection
	Key       release.VerifiedKey
	Archive   string
	Manifest  string
	Cleanup   func()
}

func addBundleFlags(flags *flag.FlagSet) (bundleFlags, *stringList) {
	var keys stringList
	options := bundleFlags{}
	options.IndexPath = flags.String("index", "", "signed release-index JSON")
	options.SignaturePath = flags.String("signature", "", "detached signify signature")
	flags.Var(&keys, "trusted-key", "trusted signify public key; repeat during rotation")
	options.TrustedKeyDir = flags.String("trusted-key-dir", "", "trusted keyring directory; defaults to the live-media keyring")
	options.Channel = flags.String("channel", "v0.1-dev", "release channel")
	options.Architecture = flags.String("architecture", runtime.GOARCH, "target architecture")
	options.InitSystem = flags.String("init-system", "", "openrc or systemd")
	options.ArchivePath = flags.String("archive", "", "offline .img.zst archive override")
	options.ManifestPath = flags.String("manifest", "", "offline release-media manifest override")
	return options, &keys
}

func verifyBundle(options bundleFlags, keys *stringList) (verifiedBundle, error) {
	if *options.IndexPath == "" ||
		(*options.InitSystem != "openrc" && *options.InitSystem != "systemd") {
		return verifiedBundle{}, errors.New("release verification requires --index and --init-system")
	}
	if (*options.ArchivePath == "") != (*options.ManifestPath == "") {
		return verifiedBundle{}, errors.New("offline mode requires both --archive and --manifest")
	}
	indexDocument, err := source.Read(*options.IndexPath, 4<<20)
	if err != nil {
		return verifiedBundle{}, err
	}
	signatureLocation := *options.SignaturePath
	if signatureLocation == "" {
		signatureLocation = *options.IndexPath + ".sig"
	}
	signature, err := source.Read(signatureLocation, 64<<10)
	if err != nil {
		return verifiedBundle{}, err
	}
	keyDocuments := make([][]byte, 0, len(*keys))
	for _, keyPath := range *keys {
		key, err := readSafeFile(keyPath, 64<<10)
		if err != nil {
			return verifiedBundle{}, err
		}
		keyDocuments = append(keyDocuments, key)
	}
	keyDirectory := *options.TrustedKeyDir
	if len(keyDocuments) == 0 && keyDirectory == "" {
		keyDirectory = defaultTrustedKeyDirectory
	}
	if keyDirectory != "" {
		directoryKeys, err := readTrustedKeyDirectory(keyDirectory)
		if err != nil {
			return verifiedBundle{}, err
		}
		keyDocuments = append(keyDocuments, directoryKeys...)
	}
	if len(keyDocuments) == 0 {
		return verifiedBundle{}, errors.New("release verification requires at least one trusted key")
	}
	verifiedKey, err := release.VerifySignify(indexDocument, signature, keyDocuments)
	if err != nil {
		return verifiedBundle{}, err
	}
	selection, err := release.ParseAndSelect(indexDocument, time.Now().UTC(), *options.Channel, *options.Architecture, *options.InitSystem)
	if err != nil {
		return verifiedBundle{}, err
	}
	archivePath := *options.ArchivePath
	manifestPath := *options.ManifestPath
	cleanup := func() {}
	if archivePath == "" {
		temporary, err := os.MkdirTemp("", "volatoo-download.")
		if err != nil {
			return verifiedBundle{}, fmt.Errorf("create artifact workspace: %w", err)
		}
		cleanup = func() { _ = os.RemoveAll(temporary) }
		archivePath, err = source.FetchBlob(*options.IndexPath, selection.Target.Archive, temporary, "release.img.zst")
		if err != nil {
			cleanup()
			return verifiedBundle{}, err
		}
		manifestPath, err = source.FetchBlob(*options.IndexPath, selection.Target.Manifest, temporary, "release.img.manifest")
		if err != nil {
			cleanup()
			return verifiedBundle{}, err
		}
	} else {
		temporary, err := os.MkdirTemp("", "volatoo-offline.")
		if err != nil {
			return verifiedBundle{}, fmt.Errorf("create offline artifact workspace: %w", err)
		}
		cleanup = func() { _ = os.RemoveAll(temporary) }
		archivePath, err = source.SnapshotBlob(archivePath, selection.Target.Archive, temporary, "release.img.zst")
		if err != nil {
			cleanup()
			return verifiedBundle{}, err
		}
		manifestPath, err = source.SnapshotBlob(manifestPath, selection.Target.Manifest, temporary, "release.img.manifest")
		if err != nil {
			cleanup()
			return verifiedBundle{}, err
		}
	}
	if err := artifact.VerifyFile(archivePath, selection.Target.Archive.Size, selection.Target.Archive.SHA256); err != nil {
		cleanup()
		return verifiedBundle{}, err
	}
	if err := artifact.VerifyFile(manifestPath, selection.Target.Manifest.Size, selection.Target.Manifest.SHA256); err != nil {
		cleanup()
		return verifiedBundle{}, err
	}
	manifestDocument, err := source.Read(manifestPath, 1<<20)
	if err != nil {
		cleanup()
		return verifiedBundle{}, err
	}
	if _, err := media.ParseAndMatch(manifestDocument, selection); err != nil {
		cleanup()
		return verifiedBundle{}, err
	}
	if err := artifact.VerifyZstdDisk(archivePath, selection.Target.Disk.Size, selection.Target.Disk.SHA256); err != nil {
		cleanup()
		return verifiedBundle{}, err
	}
	return verifiedBundle{Selection: selection, Key: verifiedKey, Archive: archivePath, Manifest: manifestPath, Cleanup: cleanup}, nil
}

func readTrustedKeyDirectory(path string) ([][]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect trusted keyring directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("trusted keyring must be a non-symlink directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read trusted keyring directory: %w", err)
	}
	if len(entries) == 0 || len(entries) > 32 {
		return nil, errors.New("trusted keyring must contain between 1 and 32 keys")
	}
	documents := make([][]byte, 0, len(entries))
	for _, entry := range entries {
		if !trustedKeyName.MatchString(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("trusted keyring contains an unsafe entry: %s", entry.Name())
		}
		document, err := readSafeFile(filepath.Join(path, entry.Name()), 64<<10)
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, nil
}

func runInstall(arguments []string) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	bundleOptions, keys := addBundleFlags(flags)
	devicePath := flags.String("device", "", "explicit target block device")
	sshKeyPath := flags.String("ssh-authorized-key", "", "administrator SSH public key file")
	noProvisionAccess := flags.Bool("no-provision-access", false, "install without administrator access")
	allowLoop := flags.Bool("allow-loop-device", false, "allow a loop target in the destructive test harness")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 || *devicePath == "" {
		return errors.New("install requires exactly one explicit --device")
	}
	if *allowLoop && os.Getenv("VOLATOO_INSTALLER_TESTING") != "1" {
		return errors.New("--allow-loop-device is restricted to the installer test harness")
	}
	if *noProvisionAccess == (*sshKeyPath != "") {
		return errors.New("choose exactly one of --ssh-authorized-key or --no-provision-access")
	}
	bundle, err := verifyBundle(bundleOptions, keys)
	if err != nil {
		return err
	}
	defer bundle.Cleanup()
	deviceInfo, err := device.Inspect(*devicePath, bundle.Selection.Target.Disk.Size, *allowLoop)
	if err != nil {
		return err
	}
	fmt.Printf("\nVolatoo installation plan\n")
	fmt.Printf("  release: %s\n", bundle.Selection.Target.ID)
	fmt.Printf("  target:  %s (%s, %d bytes, %s)\n", deviceInfo.Path, deviceInfo.Kind, deviceInfo.Size, deviceInfo.MajorMinor)
	fmt.Printf("  action:  overwrite the complete target and expand partition 4\n\n")
	fmt.Printf("WARNING: all data on %s will be permanently overwritten.\n", deviceInfo.Path)
	fmt.Printf("Type the exact device path to continue: ")
	confirmation, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, os.ErrClosed) && confirmation == "" {
		return fmt.Errorf("read installation confirmation: %w", err)
	}
	if strings.TrimSuffix(strings.TrimSuffix(confirmation, "\n"), "\r") != deviceInfo.Path {
		return errors.New("installation confirmation did not match the explicit target device")
	}
	rechecked, err := device.Inspect(*devicePath, bundle.Selection.Target.Disk.Size, *allowLoop)
	if err != nil {
		return err
	}
	if rechecked.StableID != deviceInfo.StableID || rechecked.MajorMinor != deviceInfo.MajorMinor || rechecked.Size != deviceInfo.Size {
		return errors.New("target device identity changed after confirmation")
	}
	fmt.Printf("Installing %s on %s ...\n", bundle.Selection.Target.ID, deviceInfo.Path)
	if err := installer.Apply(installer.Options{
		ArchivePath: bundle.Archive, Device: rechecked, Selection: bundle.Selection,
		SigningKeyID: bundle.Key.ID, SigningKeyHash: bundle.Key.FileSHA256,
		SSHKeyPath: *sshKeyPath, ProvisionAccess: !*noProvisionAccess,
		InstallerVersion: version, Now: time.Now().UTC(),
	}); err != nil {
		return err
	}
	fmt.Printf("Installed %s on %s.\n", bundle.Selection.Target.ID, deviceInfo.Path)
	return nil
}

func readSafeFile(path string, maximum int64) ([]byte, error) {
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
