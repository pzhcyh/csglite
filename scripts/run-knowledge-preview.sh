#!/bin/sh
# Build and run the native CSGLite knowledge preview in an isolated app home.
set -eu
project_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
if [ -x "$project_dir/local/toolchain/go/bin/go" ]; then
  task_go="$project_dir/local/toolchain/go/bin/go"
else
  task_go=$(command -v go || true)
fi
if [ -z "$task_go" ]; then
  echo 'Go 1.24.2+ is required. See docs/knowledge/README.zh-CN.md.' >&2
  exit 1
fi
(cd web && npm ci --no-audit --no-fund && npm run build)
mkdir -p internal/server/static bin local/knowledge-preview
cp -R web/dist/. internal/server/static/
CGO_ENABLED=0 "$task_go" build -o bin/csglite-knowledge-preview ./cmd/knowledge-preview
exec bin/csglite-knowledge-preview --storage "$project_dir/local/knowledge-preview" --listen 127.0.0.1:11437
