#!/bin/sh
# Launch only the bundled Apple Silicon preview; never alter another instance.
set -eu

if [ "$(/usr/bin/uname -s)" != "Darwin" ] || [ "$(/usr/bin/uname -m)" != "arm64" ]; then
  echo '此便捷启动包仅支持 Apple Silicon Mac（macOS arm64）。' >&2
  echo '其他平台请使用源码自行构建；本脚本不会安装或下载依赖。' >&2
  exit 1
fi

task_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd -P)
task_binary="$task_root/bin/csglite-knowledge-preview"
task_storage="$task_root/local/knowledge-preview"

if [ ! -f "$task_binary" ] || [ ! -x "$task_binary" ]; then
  echo '找不到可执行的 bin/csglite-knowledge-preview。请完整解压交付包并保留目录结构。' >&2
  exit 1
fi

if [ ! -x /usr/sbin/lsof ]; then
  echo '无法检查 11437 端口；未启动服务。请检查本机 lsof 是否可用。' >&2
  exit 1
fi
if /usr/sbin/lsof -nP -iTCP:11437 -sTCP:LISTEN >/dev/null 2>&1; then
  echo '11437 端口已被使用，本脚本不会停止或替换已有进程。' >&2
  echo '若已有个人知识库预览，请打开 http://localhost:11437/knowledge。' >&2
  echo '若需改用本交付包，请先在旧预览的终端按 Control-C，再重新启动。' >&2
  echo '正式 11435 实例不受影响。' >&2
  exit 1
fi

umask 077
mkdir -p "$task_storage/tmp"
export TMPDIR="$task_storage/tmp"
export TMP="$task_storage/tmp"
export TEMP="$task_storage/tmp"
cd "$task_root"

echo 'CSG Lite 个人知识库 · Apple Silicon 本地开发包（未签名）'
echo '启动后在浏览器打开：http://localhost:11437/knowledge'
echo "独立数据目录：$task_storage"
echo '在此终端按 Control-C 停止。本次启动不会操作正式 11435 实例。'
exec "$task_binary" --storage "$task_storage" --listen 127.0.0.1:11437
