#!/usr/bin/env bash
# Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
#
# Verify every Go source file carries the ArchAstro copyright header.

set -euo pipefail
cd "$(dirname "$0")/.."

missing=0
while IFS= read -r file; do
  if ! head -2 "$file" | grep -q "Copyright (c) .* ArchAstro Inc\."; then
    echo "missing header: $file" >&2
    missing=1
  fi
done < <(find platform contracttests -name '*.go')

if [[ "$missing" -ne 0 ]]; then
  echo "Add the copyright header to the files above." >&2
  exit 1
fi
echo "All Go files carry the copyright header."
