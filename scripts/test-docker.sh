#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=scripts/require-docker-context.sh
source "$repo_root/scripts/require-docker-context.sh"
volatoo_require_docker_context

docker build \
	--file "$repo_root/Dockerfile.test" \
	--tag volatoo-installer-test:0.1-dev \
	"$repo_root"

docker run --rm volatoo-installer-test:0.1-dev
