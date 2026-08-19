#!/usr/bin/env bash

set -euo pipefail

work_dir=$(mktemp -d /tmp/volatoo-installer.XXXXXX)
source_loop=
target_loop=
mounted=
cleanup()
{
	if [[ -n $mounted ]] && mountpoint -q "$mounted"; then umount "$mounted"; fi
	if [[ -n $target_loop ]]; then losetup --detach "$target_loop" 2>/dev/null || true; fi
	if [[ -n $source_loop ]]; then losetup --detach "$source_loop" 2>/dev/null || true; fi
	rm -rf -- "$work_dir"
}
trap cleanup EXIT

partition_path()
{
	local device=$1 number=$2
	if [[ $device =~ [0-9]$ ]]; then
		printf '%sp%d\n' "$device" "$number"
	else
		printf '%s%d\n' "$device" "$number"
	fi
}

wait_for_partition()
{
	local partition=$1
	for _ in {1..50}; do
		[[ -b $partition ]] && return 0
		sleep 0.1
	done
	echo "error: partition did not appear: $partition" >&2
	return 1
}

release_image=$work_dir/volatoo-v0.1-dev-test-openrc-amd64.img
truncate --size=96M "$release_image"
sgdisk --clear \
	--new=1:2048:+1M --typecode=1:ef02 --change-name=1:BIOS-BOOT \
	--new=2:4096:+8M --typecode=2:ef00 --change-name=2:VOLATOOESP \
	--new=3:20480:+16M --typecode=3:8300 --change-name=3:VOLATOO-SYSTEM \
	--new=4:53248:0 --typecode=4:8300 --change-name=4:VOLATOO-STATE \
	"$release_image" >/dev/null
source_loop=$(losetup --find --show --partscan "$release_image")
partx --add "$source_loop" 2>/dev/null || true
source_state=$(partition_path "$source_loop" 4)
rm -f -- "$source_state"
mdev -s
wait_for_partition "$source_state"
mkfs.ext4 -q -L VOLATOO-STATE "$source_state"
mounted=$work_dir/source-state
mkdir "$mounted"
mount "$source_state" "$mounted"
mkdir -p "$mounted/volatoo/config"
sync --file-system "$mounted"
umount "$mounted"
mounted=
losetup --detach "$source_loop"
source_loop=

archive=$release_image.zst
zstd -q -3 "$release_image" -o "$archive"
disk_size=$(stat --format=%s "$release_image")
disk_sha256=$(sha256sum "$release_image" | awk '{print $1}')
archive_size=$(stat --format=%s "$archive")
archive_sha256=$(sha256sum "$archive" | awk '{print $1}')
provenance_sha256=$(printf 'test provenance' | sha256sum | awk '{print $1}')
manifest=$release_image.manifest
cat >"$manifest" <<EOF
schema=org.volatoo.release-media/v2
channel=v0.1-dev
init_system=openrc
disk_file=$(basename -- "$release_image")
disk_size=$disk_size
disk_sha256=$disk_sha256
kernel_sha256=$provenance_sha256
initramfs_sha256=$provenance_sha256
rootfs_sha256=$provenance_sha256
state_sha256=$provenance_sha256
secure_boot=no
secure_boot_cert_sha256=none
uki_sha256=none
EOF
manifest_size=$(stat --format=%s "$manifest")
manifest_sha256=$(sha256sum "$manifest" | awk '{print $1}')
published_at=$(date --utc --date='-1 hour' +%Y-%m-%dT%H:%M:%SZ)
expires_at=$(date --utc --date='+1 day' +%Y-%m-%dT%H:%M:%SZ)
index=$work_dir/release-index.json
cat >"$index" <<EOF
{"schema":"org.volatoo.release-index/v1","channel":"v0.1-dev","sequence":1,"published_at":"$published_at","expires_at":"$expires_at","releases":[{"id":"v0.1.0-dev.test-openrc-amd64","architecture":"amd64","init_system":"openrc","archive":{"url":"objects/sha256/${archive_sha256:0:2}/$archive_sha256","size":$archive_size,"sha256":"$archive_sha256","format":"zstd"},"manifest":{"url":"objects/sha256/${manifest_sha256:0:2}/$manifest_sha256","size":$manifest_size,"sha256":"$manifest_sha256","format":"release-media-v2"},"disk":{"file":"$(basename -- "$release_image")","size":$disk_size,"sha256":"$disk_sha256","format":"raw-gpt"}}]}
EOF
signify -G -n -p "$work_dir/release.pub" -s "$work_dir/release.sec"
signify -S -s "$work_dir/release.sec" -m "$index" -x "$index.sig"

archive_object=$work_dir/objects/sha256/${archive_sha256:0:2}/$archive_sha256
manifest_object=$work_dir/objects/sha256/${manifest_sha256:0:2}/$manifest_sha256
mkdir -p "$(dirname -- "$archive_object")" "$(dirname -- "$manifest_object")"
ln "$archive" "$archive_object"
ln "$manifest" "$manifest_object"

release_key_digest=$(sha256sum "$work_dir/release.pub" | awk '{print $1}')
default_keyring=/usr/share/volatoo/keyring/release
mkdir -p "$default_keyring"
cp "$work_dir/release.pub" "$default_keyring/$release_key_digest.pub"
volatoo-installer verify \
	--index "$index" \
	--channel v0.1-dev \
	--architecture amd64 \
	--init-system openrc

common_arguments=(
	--index "$index"
	--signature "$index.sig"
	--trusted-key "$work_dir/release.pub"
	--channel v0.1-dev
	--architecture amd64
	--init-system openrc
	--archive "$archive"
	--manifest "$manifest"
)
volatoo-installer verify "${common_arguments[@]}"

target_image=$work_dir/target.img
truncate --size=160M "$target_image"
target_loop=$(losetup --find --show --partscan "$target_image")
ssh_key=$work_dir/authorized_keys
printf '%s\n' 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIN8XFE9WwjHvSxBSnbiuupCmyvRetPJYcHARXeTwdtLb volatoo-installer-test' >"$ssh_key"
printf '%s\n' "$target_loop" | VOLATOO_INSTALLER_TESTING=1 volatoo-installer install \
	"${common_arguments[@]}" \
	--device "$target_loop" \
	--ssh-authorized-key "$ssh_key" \
	--allow-loop-device

target_state=$(partition_path "$target_loop" 4)
wait_for_partition "$target_state"
e2fsck -fn "$target_state"
mounted=$work_dir/target-state
mkdir "$mounted"
mount -o ro "$target_state" "$mounted"
cmp "$ssh_key" "$mounted/volatoo/config/access/authorized_keys"
[[ $(stat --format=%a "$mounted/volatoo/config/access/authorized_keys") == 600 ]]
receipt=$mounted/volatoo/install/receipt-v1.json
[[ -f $receipt && ! -L $receipt ]]
grep -Fq '"schema": "org.volatoo.install-receipt/v1"' "$receipt"
grep -Fq '"release_id": "v0.1.0-dev.test-openrc-amd64"' "$receipt"
grep -Fq '"disk_sha256": "'"$disk_sha256"'"' "$receipt"
umount "$mounted"

before=$(sha256sum "$target_image" | awk '{print $1}')
cp "$index.sig" "$work_dir/tampered.sig"
printf x >>"$work_dir/tampered.sig"
tampered_arguments=("${common_arguments[@]}")
tampered_arguments[3]=$work_dir/tampered.sig
if VOLATOO_INSTALLER_TESTING=1 volatoo-installer install \
	"${tampered_arguments[@]}" \
	--device "$target_loop" --no-provision-access --allow-loop-device \
	</dev/null >"$work_dir/tampered.stdout" 2>"$work_dir/tampered.stderr"
then
	echo "error: installer accepted a changed release-index signature" >&2
	exit 1
fi
grep -Eq 'not a detached signify file|invalid signify payload' "$work_dir/tampered.stderr"
[[ $(sha256sum "$target_image" | awk '{print $1}') == "$before" ]]

if printf '/dev/not-the-target\n' | VOLATOO_INSTALLER_TESTING=1 volatoo-installer install \
	"${common_arguments[@]}" \
	--device "$target_loop" --no-provision-access --allow-loop-device \
	>"$work_dir/confirmation.stdout" 2>"$work_dir/confirmation.stderr"
then
	echo "error: installer accepted a mismatched device confirmation" >&2
	exit 1
fi
grep -Fq 'confirmation did not match' "$work_dir/confirmation.stderr"
[[ $(sha256sum "$target_image" | awk '{print $1}') == "$before" ]]

mount -o ro,noload "$target_state" "$mounted"
mounted_before=$(sha256sum "$target_image" | awk '{print $1}')
if VOLATOO_INSTALLER_TESTING=1 volatoo-installer install \
	"${common_arguments[@]}" \
	--device "$target_loop" --no-provision-access --allow-loop-device \
	</dev/null >"$work_dir/mounted.stdout" 2>"$work_dir/mounted.stderr"
then
	echo "error: installer accepted a mounted target" >&2
	exit 1
fi
grep -Fq 'target device or one of its partitions is mounted' "$work_dir/mounted.stderr"
[[ $(sha256sum "$target_image" | awk '{print $1}') == "$mounted_before" ]]
umount "$mounted"
mounted=

echo "Volatoo installer privileged integration test passed"
