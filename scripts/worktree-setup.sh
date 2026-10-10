#!/usr/bin/env bash
set -Eeuo pipefail

WORKTREE="${CODEX_WORKTREE_PATH:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$WORKTREE"

step() { printf '[lingdoc] %s\n' "$1"; }

step "工作树: $WORKTREE"
step "下载 Go 模块"
go mod download

if [[ "${LINGDOC_SKIP_FRONTEND:-0}" != "1" ]]; then
  [[ -f frontend/package-lock.json ]] || { echo "frontend/package-lock.json 不存在，无法使用 npm ci" >&2; exit 1; }
  step "安装前端锁定依赖"
  npm ci --prefix frontend
else
  step "跳过前端依赖（LINGDOC_SKIP_FRONTEND=1）"
fi

if [[ "${LINGDOC_SKIP_PYTHON:-0}" != "1" && -f mcp-server/requirements.txt ]]; then
  PYTHON_BIN="${PYTHON_BIN:-python3}"
  command -v "$PYTHON_BIN" >/dev/null 2>&1 || { echo "未找到 $PYTHON_BIN；设置 LINGDOC_SKIP_PYTHON=1 可跳过 Python 依赖" >&2; exit 1; }
  if [[ ! -x .venv/bin/python ]]; then
    step "创建 Python 虚拟环境"
    "$PYTHON_BIN" -m venv .venv
  fi
  step "安装 MCP Python 依赖"
  .venv/bin/python -m pip install -r mcp-server/requirements.txt
elif [[ "${LINGDOC_SKIP_PYTHON:-0}" == "1" ]]; then
  step "跳过 Python 依赖（LINGDOC_SKIP_PYTHON=1）"
else
  step "未发现 mcp-server/requirements.txt，跳过 Python 依赖"
fi

if [[ ! -e .env && -f .env.example ]]; then
  cp .env.example .env
  step "已从 .env.example 创建 .env；请按本地环境补齐占位配置"
else
  step "保留现有 .env"
fi

step "工作树初始化完成"
