#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
[[ $(docker context show) == orbstack ]] || {
	echo "error: Docker context must be orbstack" >&2
	exit 1
}

docker build \
	--file "$repo_root/Dockerfile.test" \
	--tag volatoo-installer-test:0.1-dev \
	"$repo_root"

docker run --rm volatoo-installer-test:0.1-dev
