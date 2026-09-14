#!/usr/bin/env bash

# Run from the repository root, using the same artifact checks locally and in CI.
set -euo pipefail

release_dist=$(cd "${GO42X_RELEASE_DIST:-.build/dist}" && pwd)
release_version=$(jq -er '.version | select(type == "string" and length > 0)' "$release_dist/metadata.json")
source_commit=$(git rev-parse HEAD)
jq -e --arg commit "$source_commit" '.commit == $commit' "$release_dist/metadata.json" > /dev/null

# Check the complete set of archives, Linux packages, and SBOMs before testing.
for release_os in linux darwin; do
  for release_arch in amd64 arm64; do
    archive=$(jq -er --arg os "$release_os" --arg arch "$release_arch" '
      [.[] | select(.type == "Archive" and .goos == $os and .goarch == $arch)] |
      if length == 1 then .[0].name else error("expected exactly one archive for " + $os + "/" + $arch) end
    ' "$release_dist/artifacts.json")
    test -s "$release_dist/$archive"
    jq -e '.spdxVersion and (.packages | length > 0)' "$release_dist/$archive.sbom.json" > /dev/null
    if [ "$release_os" = linux ]; then
      for package_format in deb rpm apk; do
        package=$(jq -er --arg arch "$release_arch" --arg format "$package_format" '
          [.[] | select(.type == "Linux Package" and .goos == "linux" and
            .goarch == $arch and .extra.Format == $format)] |
          if length == 1 then .[0].name else error("expected exactly one " + $format + " package for " + $arch) end
        ' "$release_dist/artifacts.json")
        test -s "$release_dist/$package"
      done
    fi
  done
done

(
  cd "$release_dist"
  shasum -a 256 --check checksums.txt
  # Each distributed artifact must also appear exactly once in checksums.txt.
  jq -er '.[] | select(.type == "Archive" or .type == "Linux Package" or .type == "SBOM") | .name' artifacts.json |
    while IFS= read -r artifact; do
      awk -v name="$artifact" '$2 == name { count++ } END { exit count != 1 }' checksums.txt
    done
)

native_os=$(go env GOHOSTOS)
native_arch=$(go env GOHOSTARCH)
native_archive=$(jq -er --arg os "$native_os" --arg arch "$native_arch" '
  [.[] | select(.type == "Archive" and .goos == $os and .goarch == $arch)] |
  if length == 1 then .[0].name else error("no unique archive for this runner") end
' "$release_dist/artifacts.json")

extracted_dir=$(mktemp -d "${TMPDIR:-/tmp}/go42x-release-e2e.XXXXXX")
trap 'rm -rf "$extracted_dir"' EXIT
tar -xzf "$release_dist/$native_archive" -C "$extracted_dir"
test -x "$extracted_dir/go42x"
cmp LICENSE "$extracted_dir/LICENSE"
cmp README.md "$extracted_dir/README.md"

echo "Testing $native_archive ($release_version) on $native_os/$native_arch"
export GO42X_E2E_BINARY="$extracted_dir/go42x"
export GO42X_E2E_VERSION="$release_version"
go test -count=1 -v -race -timeout=5m ./tests/e2e/...
