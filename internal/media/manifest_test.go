package media

import (
	"strings"
	"testing"

	"github.com/Volatoo/installer/internal/release"
)

func TestParseAndMatch(t *testing.T) {
	digest := strings.Repeat("a", 64)
	selection := release.Selection{
		Index:  release.Index{Channel: "v0.1-dev"},
		Target: release.Target{InitSystem: "openrc", Disk: release.Disk{File: "volatoo.img", Size: 4096, SHA256: digest}},
	}
	document := []byte("schema=org.volatoo.release-media/v2\n" +
		"channel=v0.1-dev\ninit_system=openrc\ndisk_file=volatoo.img\ndisk_size=4096\n" +
		"disk_sha256=" + digest + "\nkernel_sha256=" + digest + "\ninitramfs_sha256=" + digest +
		"\nrootfs_sha256=" + digest + "\nstate_sha256=" + digest +
		"\nsecure_boot=no\nsecure_boot_cert_sha256=none\nuki_sha256=none\n")
	if _, err := ParseAndMatch(document, selection); err != nil {
		t.Fatal(err)
	}
	changed := []byte(strings.Replace(string(document), "init_system=openrc", "init_system=systemd", 1))
	if _, err := ParseAndMatch(changed, selection); err == nil {
		t.Fatal("mismatched manifest was accepted")
	}
	unknown := append(document, []byte("unknown=value\n")...)
	if _, err := ParseAndMatch(unknown, selection); err == nil {
		t.Fatal("unknown field was accepted")
	}
}
