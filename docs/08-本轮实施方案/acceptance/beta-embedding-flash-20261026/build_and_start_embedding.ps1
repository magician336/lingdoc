$ErrorActionPreference = 'Stop'

# ---------------------------------------------------------------------------
# Build the embedding service from the CURRENT head and start it with the
# queue isolation of #670 in effect.
#
# The binary is built from a worktree whose HEAD tree is IDENTICAL to
# origin/main (verified below), so the running process carries exactly the
# merged main content -- including #670 (queue isolation) and #671 (the
# embedding HTTP error recovery).
#
# LINGDOC_ASYNQ_REDIS_DB is set EXPLICITLY, so the startup log shows
# queue_redis_db=2 queue_db_source=queue-db-env: the fact that the queue
# database came from the queue's own setting, not from the shared REDIS_DB.
# ---------------------------------------------------------------------------

$WorkTree = 'D:\wt\lingdoc-beta-status'
$Root     = 'D:\wt\lingdoc-484-evidence'
$MainRepo = 'D:\code_warehouse\Projects\lingdoc'
$EnvFile  = Join-Path $MainRepo '.env'
$Stamp    = (Get-Date -Format 'yyyyMMdd-HHmmss')
$Exe      = Join-Path $Root ("weknora-embedding-$Stamp.exe")
$Port     = 18096

$dbPath     = Join-Path $Root 'data\lingdoc-484-final.db'
$embedLedger = Join-Path $Root 'ledger\qwen-embedding-budget.json'
$chatLedger  = Join-Path $Root 'ledger\qwen-beta-sqlite.json'

$env:GOCACHE    = 'D:\wt\gocache-655'
$env:CGO_CFLAGS = '-I C:\Users\ASUS\.sqlite-headers'

Write-Output '=== 1. the source tree is exactly origin/main ==='
Push-Location $WorkTree
try {
    $head = (git rev-parse HEAD).Trim()
    $headTree = (git rev-parse 'HEAD^{tree}').Trim()
    $mainTree = (git rev-parse 'origin/main^{tree}').Trim()
    $dirty = git status --porcelain
    Write-Output ("head             : " + $head)
    Write-Output ("origin/main sha  : " + (git rev-parse origin/main).Trim())
    Write-Output ("HEAD tree        : " + $headTree)
    Write-Output ("origin/main tree : " + $mainTree)
    if ($headTree -ne $mainTree) { throw 'the HEAD tree differs from origin/main' }
    if ($dirty) { Write-Output 'WORKTREE NOT CLEAN:'; $dirty; throw 'the worktree is not clean' }
    Write-Output 'HEAD tree == origin/main tree; the worktree is clean'

    Write-Output '=== 2. build the server binary ==='
    go build -tags sqlite_fts5 -o $Exe ./cmd/server
    if ($LASTEXITCODE -ne 0) { throw 'the build failed' }
} finally {
    Pop-Location
}
Write-Output ("exe        : " + $Exe)
Write-Output ("exe sha256 : " + (Get-FileHash -LiteralPath $Exe -Algorithm SHA256).Hash)
Write-Output ("exe bytes  : " + (Get-Item -LiteralPath $Exe).Length)

Write-Output '=== 3. the budget state, reported and NOT reset ==='
foreach ($lp in @($embedLedger, $embedLedger + '.calls.json', $chatLedger, $chatLedger + '.calls.json')) {
    if (Test-Path -LiteralPath $lp) {
        $item = Get-Item -LiteralPath $lp
        $sha = (Get-FileHash -Algorithm SHA256 -LiteralPath $lp).Hash.ToLower()
        Write-Output ("LEDGER_PRESENT path=" + $lp + " bytes=" + $item.Length + " sha256=" + $sha)
        $doc = [System.IO.File]::ReadAllText($lp) | ConvertFrom-Json
        Write-Output ("  requests=" + @($doc.requests).Count + " blocked=" + [string]$doc.blocked +
                      " reserved_tokens=" + [string]$doc.reserved_tokens +
                      " reserved_nano_cny=" + [string]$doc.reserved_nano_cny +
                      " profile=" + [string]$doc.profile)
    } else {
        Write-Output ("LEDGER_ABSENT path=" + $lp)
    }
}

Write-Output '=== 4. start the embedding instance ==='
$map = @{}
foreach ($line in [System.IO.File]::ReadAllLines($EnvFile)) {
    if ($line -match '^\s*([A-Za-z_][A-Za-z0-9_]*)=(.*)$') { $map[$matches[1]] = $matches[2] }
}
$jwt = $map['JWT_SECRET']
if ([string]::IsNullOrWhiteSpace($jwt)) { throw 'missing .env key: JWT_SECRET' }

New-Item -ItemType Directory -Force -Path (Join-Path $Root 'logs'), (Join-Path $Root 'ledger') | Out-Null
$logPath = Join-Path $Root ("logs\server-embedding-$Stamp.log")
$llmDir  = Join-Path $Root 'logs\llm_debug_embedding'

$env:GIN_MODE      = 'release'
$env:LOG_LEVEL     = 'info'
$env:LOG_PATH      = $logPath
$env:LLM_DEBUG_LOG = $llmDir

$env:AUTO_MIGRATE       = 'true'
$env:AUTO_RECOVER_DIRTY = 'false'

$env:DB_DRIVER = 'sqlite'
$env:DB_PATH   = $dbPath

$env:REDIS_ADDR     = '127.0.0.1:6381'
$env:REDIS_PASSWORD = ''
$env:REDIS_DB       = '2'
$env:REDIS_PREFIX   = 'iso484sq:embed:'
# #670: the async task queue gets its OWN database index. The chat instances
# live on REDIS_DB=1, so the embedding instance's document-processing tasks
# cannot be picked up by a chat-profile worker.
$env:LINGDOC_ASYNQ_REDIS_DB = '2'

$env:STORAGE_TYPE          = 'local'
$env:LOCAL_STORAGE_BASE_DIR = Join-Path $Root 'files-sqlite'

$env:RETRIEVE_DRIVER = 'sqlite'

$env:SERVER_PORT = "$Port"
$env:JWT_SECRET = $jwt

$env:SYSTEM_AES_KEY     = 'm484-iso-aes-key-32-bytes-lng!!!'
$env:SYSTEM_SIGNING_KEY = 'm484-iso-sign-key-32-bytes-lng!!'

$env:OLLAMA_OPTIONAL = 'true'
$env:WEKNORA_SANDBOX_DOCKER_ENABLED = 'false'
$env:WEKNORA_HOUSEKEEPING_ENABLED  = 'false'
$env:CONCURRENCY_POOL_SIZE = '5'

# The authorized embedding profile and its OWN ledger.
$env:LINGDOC_T10_ACCEPTANCE_PROFILE = 'lingdoc-qwen-embedding-budget-20261025'
$env:LINGDOC_T10_ACCEPTANCE_LEDGER  = $embedLedger

Remove-Item Env:DASHSCOPE_API_KEY -ErrorAction SilentlyContinue
Remove-Item Env:DEEPSEEK_API_KEY -ErrorAction SilentlyContinue
Remove-Item Env:BUILTIN_MODELS_CONFIG -ErrorAction SilentlyContinue

$busy = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
if ($busy) { throw "port $Port is already in use" }

$out = $logPath + '.stdout.log'
$err = $logPath + '.stderr.log'
$proc = Start-Process -FilePath $Exe -WorkingDirectory $WorkTree `
    -RedirectStandardOutput $out -RedirectStandardError $err `
    -WindowStyle Hidden -PassThru
Set-Content -Path (Join-Path $Root 'server-embedding.pid') -Value $proc.Id -Encoding ascii

Write-Output ("started pid=" + $proc.Id + " port=" + $Port + " at " + (Get-Date -Format o))

$base = "http://127.0.0.1:$Port/api/v1"
$up = $false
for ($i = 0; $i -lt 90; $i++) {
    try {
        $h = Invoke-RestMethod -Uri "$base/auth/config" -TimeoutSec 5
        $up = $true
        break
    } catch {
        Start-Sleep -Seconds 1
    }
}
if (-not $up) {
    Write-Output 'SERVER_DID_NOT_COME_UP'
    Write-Output (([System.IO.File]::ReadAllText($err)) -replace '\s+', ' ')
    exit 1
}
Write-Output 'server_reachable=true'
Write-Output ("log=" + $logPath)

Write-Output '=== 5. the queue identity in the startup log ==='
$logText = [System.IO.File]::ReadAllText($logPath)
$queueLines = [regex]::Matches($logText, '[^\r\n]*queue_redis_db=[^\r\n]*')
Write-Output ("queue identity lines: " + $queueLines.Count)
foreach ($m in $queueLines) { Write-Output ("  " + $m.Value.Trim()) }
$source = [regex]::Matches($logText, 'queue_db_source=([a-z-]+)')
$kinds = @{}
foreach ($s in $source) { $kinds[$s.Groups[1].Value] = $true }
Write-Output ("queue_db_source values: " + (($kinds.Keys | Sort-Object) -join ', '))
