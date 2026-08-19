#!/usr/bin/env bash

set -euo pipefail

usage()
{
	echo "Usage: scripts/build-docker.sh --version VERSION OUTPUT" >&2
}

version=
if (( $# == 3 )) && [[ $1 == --version ]]; then
	version=$2
	output=$3
else
	usage
	exit 2
fi
[[ $version =~ ^[0-9]+.[0-9]+.[0-9]+(-[A-Za-z0-9.-]+)?$ ]] || {
	echo "error: invalid installer version" >&2
	exit 2
}
repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=scripts/require-docker-context.sh
source "$repo_root/scripts/require-docker-context.sh"
volatoo_require_docker_context
output_name=$(basename -- "$output")
output_directory=$(cd -- "$(dirname -- "$output")" && pwd)
[[ $output_name != . && $output_name != .. && ! -e $output ]] || {
	echo "error: output already exists or has an unsafe name: $output" >&2
	exit 1
}
staging=$(mktemp -d "$output_directory/.volatoo-installer-build.XXXXXX")
cleanup()
{
	if [[ -f $staging/volatoo-installer ]]; then rm -f -- "$staging/volatoo-installer"; fi
	rmdir "$staging"
}
trap cleanup EXIT

docker build \
	--platform linux/amd64 \
	--build-arg "VERSION=$version" \
	--file "$repo_root/Dockerfile.build" \
	--target artifact \
	--output "type=local,dest=$staging" \
	"$repo_root"

[[ -f $staging/volatoo-installer && ! -L $staging/volatoo-installer && -x $staging/volatoo-installer ]] || {
	echo "error: builder did not export a safe installer executable" >&2
	exit 1
}
mv "$staging/volatoo-installer" "$output_directory/$output_name"
trap - EXIT
rmdir "$staging"
echo "built $output_directory/$output_name"
