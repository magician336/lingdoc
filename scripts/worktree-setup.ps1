$ErrorActionPreference = "Stop"

$worktree = if ($env:CODEX_WORKTREE_PATH) { $env:CODEX_WORKTREE_PATH } else { (Resolve-Path (Join-Path $PSScriptRoot "..")).Path }
Set-Location $worktree

function Write-Step([string]$Message) {
    Write-Host "[lingdoc] $Message"
}

Write-Step "工作树: $worktree"

Write-Step "下载 Go 模块"
go mod download

if ($env:LINGDOC_SKIP_FRONTEND -ne "1") {
    if (!(Test-Path "frontend/package-lock.json")) {
        throw "frontend/package-lock.json 不存在，无法使用 npm ci"
    }
    Write-Step "安装前端锁定依赖"
    npm ci --prefix frontend
} else {
    Write-Step "跳过前端依赖（LINGDOC_SKIP_FRONTEND=1）"
}

if ($env:LINGDOC_SKIP_PYTHON -ne "1" -and (Test-Path "mcp-server/requirements.txt")) {
    $python = Get-Command py -ErrorAction SilentlyContinue
    if ($python) {
        $pythonCommand = "py"
        $pythonArgs = @("-3")
    } else {
        $python = Get-Command python -ErrorAction SilentlyContinue
        if (!$python) { throw "未找到 py 或 python；设置 LINGDOC_SKIP_PYTHON=1 可跳过 Python 依赖" }
        $pythonCommand = "python"
        $pythonArgs = @()
    }

    if (!(Test-Path ".venv/Scripts/python.exe")) {
        Write-Step "创建 Python 虚拟环境"
        & $pythonCommand @pythonArgs -m venv .venv
    }

    Write-Step "安装 MCP Python 依赖"
    & ".venv/Scripts/python.exe" -m pip install -r mcp-server/requirements.txt
} elseif ($env:LINGDOC_SKIP_PYTHON -eq "1") {
    Write-Step "跳过 Python 依赖（LINGDOC_SKIP_PYTHON=1）"
}

if (!(Test-Path ".env") -and (Test-Path ".env.example")) {
    Copy-Item ".env.example" ".env"
    Write-Step "已从 .env.example 创建 .env；请按本地环境补齐占位配置"
} else {
    Write-Step "保留现有 .env"
}

Write-Step "工作树初始化完成"
