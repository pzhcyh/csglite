#!/usr/bin/env python3
"""Package the personal knowledge source and Apple Silicon binary, without data.

Python 3.9+, standard library only. This script does not build or launch anything.
Usage: python3 scripts/package-knowledge-delivery.py /absolute/path/delivery.zip
"""

import argparse
import os
from pathlib import Path
import stat
import struct
import zipfile


ARCHIVE_ROOT = "CSGLite-Personal-Knowledge"
SOURCE_DIRS = ("cmd", "internal", "pkg", "ee", "openapi", "docs", "scripts", "web")
ROOT_FILES = ("LICENSE", "README.md", "AGENTS.md", "Makefile", "go.mod", "go.sum")
EXCLUDED_DIRS = {
    "local", "node_modules", "dist", "coverage", "__pycache__",
    "test-results", "test-reports", "playwright-report", "reports", "outputs",
}
EXCLUDED_SUFFIXES = {
    ".pyc", ".pyo", ".log", ".sqlite", ".sqlite3", ".db", ".env",
    ".pem", ".key", ".p12", ".pfx", ".zip", ".tgz", ".gz", ".bak",
}
EXCLUDED_FILES = {
    "config.json", "state.json", "credentials.json", "tokens.json",
    "secrets.json", "connections.json", "coverage.xml", "junit.xml",
    "coverage-final.json", "lcov.info",
}
REQUIRED_FILES = (
    "LICENSE", "ee/LICENSE", "go.mod", "go.sum", "cmd/knowledge-preview/main.go",
    "internal/server/static/index.html", "web/package.json", "web/package-lock.json",
    "docs/knowledge/README.zh-CN.md", "docs/knowledge/DELIVERY.zh-CN.md",
    "docs/knowledge/CSGCLAW.zh-CN.md", "scripts/run-knowledge-preview.sh",
    "scripts/start-knowledge-delivery.command", "scripts/package-knowledge-delivery.py",
    "bin/csglite-knowledge-preview",
)


def excluded_file(path):
    name = path.name.lower()
    if name.startswith(".") or name in EXCLUDED_FILES or path.suffix.lower() in EXCLUDED_SUFFIXES:
        return True
    if name.startswith(("id_rsa", "id_ed25519")) or name.endswith(("~", ".db-wal", ".db-shm")):
        return True
    if path.suffix.lower() in {".json", ".txt", ".yaml", ".yml"}:
        return any(word in name for word in ("credential", "secret", "access_token"))
    return False


def collect_files(root):
    """Use an explicit source allowlist, not Git tracking or .gitignore rules."""
    files = []
    for name in ROOT_FILES:
        path = root / name
        if path.is_file() and not path.is_symlink():
            files.append(path)
    for name in SOURCE_DIRS:
        base = root / name
        if not base.is_dir() or base.is_symlink():
            continue
        for directory, dirs, names in os.walk(str(base), followlinks=False):
            current = Path(directory)
            dirs[:] = sorted(
                child for child in dirs
                if not child.startswith(".")
                and child.lower() not in EXCLUDED_DIRS
                and not (current / child).is_symlink()
            )
            for child in sorted(names):
                path = current / child
                if path.is_file() and not path.is_symlink() and not excluded_file(path):
                    files.append(path)
    binary = root / "bin/csglite-knowledge-preview"
    if binary.is_file() and not binary.is_symlink():
        files.append(binary)
    return sorted(files, key=lambda path: path.relative_to(root).as_posix())


def check_files(root, files):
    included = {path.relative_to(root).as_posix() for path in files}
    missing = sorted(set(REQUIRED_FILES) - included)
    if missing:
        raise ValueError("缺少交付文件：" + ", ".join(missing))
    if not any(name.startswith("internal/server/static/assets/") and name.endswith(".js") for name in included):
        raise ValueError("缺少嵌入前端 JavaScript；请先构建并同步静态资源。")
    with (root / "bin/csglite-knowledge-preview").open("rb") as source:
        header = source.read(8)
    if len(header) != 8 or struct.unpack("<II", header) != (0xFEEDFACF, 0x0100000C):
        raise ValueError("交付二进制必须是 macOS arm64 Mach-O；此脚本不会代为构建。")


def package(root, output):
    files = collect_files(root)
    check_files(root, files)
    if output.exists() or output.is_symlink():
        raise ValueError("输出文件已存在；请指定新的 ZIP 路径，避免覆盖已有交付。")
    output.parent.mkdir(parents=True, exist_ok=True)
    created = False
    try:
        with zipfile.ZipFile(str(output), "x", compression=zipfile.ZIP_DEFLATED, compresslevel=6, strict_timestamps=False) as archive:
            created = True
            for path in files:
                relative = path.relative_to(root).as_posix()
                entry = zipfile.ZipInfo.from_file(str(path), ARCHIVE_ROOT + "/" + relative, strict_timestamps=False)
                entry.create_system = 3
                entry.compress_type = zipfile.ZIP_DEFLATED
                executable = relative == "bin/csglite-knowledge-preview" or path.suffix in {".sh", ".command"}
                entry.external_attr = (stat.S_IFREG | (0o755 if executable else 0o644)) << 16
                with path.open("rb") as source, archive.open(entry, "w", force_zip64=True) as target:
                    while True:
                        chunk = source.read(1024 * 1024)
                        if not chunk:
                            break
                        target.write(chunk)
        print("交付包：" + str(output))
        print("文件数：{}；不含运行数据、模型、连接凭证或依赖缓存。".format(len(files)))
    except Exception:
        if created:
            output.unlink()
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path, help="新建 ZIP 文件的输出路径")
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    output = args.output.expanduser().absolute()
    if output.suffix.lower() != ".zip":
        parser.error("输出文件必须使用 .zip 扩展名。")
    try:
        package(root, output)
    except (OSError, ValueError, zipfile.BadZipFile) as error:
        parser.exit(1, "打包失败：{}\n".format(error))


if __name__ == "__main__":
    main()
