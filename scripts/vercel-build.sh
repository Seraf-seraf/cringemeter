#!/bin/sh
set -eu

if [ "${VERCEL_GIT_COMMIT_REF:-}" != "main" ]; then
    printf '%s\n' 'Skipping migrations outside main.'
    exit 0
fi

: "${TURSO_DATABASE_URL:?TURSO_DATABASE_URL is required for main migrations}"
: "${TURSO_AUTH_TOKEN:?TURSO_AUTH_TOKEN is required for main migrations}"

migration_go=go
if ! command -v go >/dev/null 2>&1; then
    migration_toolchain_dir=$(mktemp -d)
    trap 'rm -rf "$migration_toolchain_dir"' 0
    curl --fail --silent --show-error --location --connect-timeout 10 --max-time 120 \
        --output "$migration_toolchain_dir/go.tar.gz" \
        'https://go.dev/dl/go1.22.12.linux-amd64.tar.gz'
    printf '%s  %s\n' \
        '4fa4f869b0f7fc6bb1eb2660e74657fbf04cdd290b5aef905585c86051b34d43' \
        "$migration_toolchain_dir/go.tar.gz" | sha256sum --check --status
    tar -xzf "$migration_toolchain_dir/go.tar.gz" -C "$migration_toolchain_dir"
    migration_go="$migration_toolchain_dir/go/bin/go"
fi

printf '%s\n' 'Applying database migrations for main.'
"$migration_go" run ./cmd/migrate
