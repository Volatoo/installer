#!/usr/bin/env bash

set -euo pipefail

[[ $(docker context show) == orbstack ]] || {
	echo "error: Docker context must be orbstack" >&2
	exit 1
}
repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
work_dir=$(mktemp -d /tmp/volatoo-installer-reproducible.XXXXXX)
cleanup()
{
	rm -f -- "$work_dir/installer-a" "$work_dir/installer-b"
	rmdir "$work_dir"
}
trap cleanup EXIT

"$repo_root/scripts/build-docker.sh" --version 0.1.0-dev "$work_dir/installer-a"
"$repo_root/scripts/build-docker.sh" --version 0.1.0-dev "$work_dir/installer-b"
cmp "$work_dir/installer-a" "$work_dir/installer-b"
sha256sum "$work_dir/installer-a"
echo "Volatoo installer reproducible build test passed"
