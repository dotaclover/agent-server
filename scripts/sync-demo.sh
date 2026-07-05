#!/bin/bash
# scripts/sync-demo.sh
# 通过 WSL rsync 同步 Go 源码 + JS + 前端 + 配置到 go-agent-demo
# 不覆盖目标的 .git/ .gitignore README.md CLAUDE.md TODO.md docs/
#
# 用法（PowerShell 中）:
#   wsl bash -c "cd /mnt/c/code/go-agent-studio && bash scripts/sync-demo.sh"
#   wsl bash -c "cd /mnt/c/code/go-agent-studio && bash scripts/sync-demo.sh --dry-run"

set -euo pipefail

SRC="/mnt/c/code/go-agent-studio"
DST="/mnt/c/code/go-agent-demo"
DRY_RUN=""

if [[ "${1:-}" == "--dry-run" ]]; then
    DRY_RUN="--dry-run"
    echo "=== 预览模式（不实际同步）==="
fi

if [[ ! -d "$DST" ]]; then
    echo "目标目录不存在: $DST"
    echo "请先创建: mkdir -p $DST"
    exit 1
fi

echo "源: $SRC"
echo "目标: $DST"
echo ""

rsync -avz --delete $DRY_RUN \
    --exclude='.git/' \
    --exclude='.gitignore' \
    --exclude='.github/' \
    --exclude='.workflow/' \
    --exclude='.env' \
    --exclude='LICENSE' \
    --exclude='DISCLAIMER.md' \
    --exclude='docs/' \
    --exclude='workdocs/' \
    --exclude='vendor/' \
    --exclude='logs/' \
    --exclude='bin/' \
    --exclude='*.exe' \
    --exclude='go-agent-studio-linux*' \
    --exclude='*.db' \
    --exclude='*.db-shm' \
    --exclude='*.db-wal' \
    --exclude='coverage.out' \
    --exclude='node_modules/' \
    --exclude='data/domains/labor_law.bge/' \
    --exclude='data/agent_studio.db*' \
    "$SRC/" "$DST/"

echo ""
if [[ -n "$DRY_RUN" ]]; then
    echo "=== 预览完成，去掉 --dry-run 可实际同步 ==="
else
    echo "=== 同步完成 ==="
fi
