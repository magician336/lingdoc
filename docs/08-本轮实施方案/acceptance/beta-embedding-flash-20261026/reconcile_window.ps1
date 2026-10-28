$ErrorActionPreference = 'Stop'

$WorkTree   = 'D:\wt\lingdoc-beta-status'
$Evidence   = 'D:\wt\lingdoc-485-evidence\embedding-run\flash-run'
$Ledger     = 'D:\wt\lingdoc-484-evidence\ledger\qwen-embedding-budget.json.calls.json'
$Preserved  = Join-Path $Evidence 'ledger-before-reconcile.calls.json'
$EvidenceJSON = Join-Path $Evidence 'reconcile-evidence.json'
$Tool       = Join-Path $Evidence 'acceptance-budget-reconcile.exe'
$OldPidFile = 'D:\wt\lingdoc-484-evidence\server-embedding.pid'

$env:GOCACHE = 'D:\wt\gocache-619'
$env:CGO_CFLAGS = '-I C:\Users\ASUS\.sqlite-headers'

Write-Output '=== 1. preserve the frozen ledger bytes (a copy, never a rewrite) ==='
$before = (Get-FileHash -Algorithm SHA256 -LiteralPath $Ledger).Hash
Copy-Item -LiteralPath $Ledger -Destination $Preserved -Force
$copied = (Get-FileHash -Algorithm SHA256 -LiteralPath $Preserved).Hash
Write-Output ("ledger sha256 before   : " + $before)
Write-Output ("preserved copy sha256  : " + $copied)
if ($before -ne $copied) { throw 'the preserved copy does not match the live ledger' }

Write-Output '=== 2. build the reconcile tool from the current head ==='
Push-Location $WorkTree
try {
    Write-Output ("head: " + (git rev-parse HEAD))
    go build -o $Tool ./cmd/acceptance-budget-reconcile
    if ($LASTEXITCODE -ne 0) { throw 'the reconcile tool did not build' }
} finally {
    Pop-Location
}
Write-Output ("tool: " + $Tool)

Write-Output '=== 3. stop the OLD embedding instance (pre-#670 binary) ==='
$oldPid = $null
if (Test-Path -LiteralPath $OldPidFile) {
    $oldPid = [int]((Get-Content -LiteralPath $OldPidFile -Raw).Trim())
}
$proc = Get-Process -Id $oldPid -ErrorAction SilentlyContinue
if ($proc) {
    Write-Output ("stopping pid " + $oldPid + " (" + $proc.ProcessName + ")")
    Stop-Process -Id $oldPid -Force
    Start-Sleep -Seconds 3
} else {
    Write-Output ("the old instance (pid " + $oldPid + ") is not running")
}
$still = Get-Process -Id $oldPid -ErrorAction SilentlyContinue
if ($still) { throw 'the old embedding instance is still running' }
Write-Output 'old embedding instance stopped'

Write-Output '=== 4. reconcile the frozen window (local ledger operation) ==='
$reason = 'the provider answered HTTP 403 insufficient_quota on 2026-10-25 (free tier exhausted); the request produced no usage, so the window is settled at the full reserved upper bound and the authorized window is reopened for the Flash model'
# The gate requires a LOWERCASE sha256. Get-FileHash prints uppercase, so the
# expected digest is taken from the evidence file (written in lowercase) and
# checked against the live file here.
$expected = (Get-Content -LiteralPath $EvidenceJSON -Raw -Encoding UTF8 | ConvertFrom-Json).ledger_sha256
$liveLower = $before.ToLower()
Write-Output ("expected sha256 (lowercase): " + $expected)
if ($expected -ne $liveLower) { throw 'the evidence digest does not match the live ledger' }
& $Tool -ledger $Ledger -profile 'lingdoc-qwen-embedding-budget-20261025' -expected-ledger-sha256 $expected -request 1 -reason $reason -evidence $EvidenceJSON -apply -confirm-full-reserve-charge
$code = $LASTEXITCODE
Write-Output ("reconcile exit code: " + $code)
if ($code -ne 0) { throw 'the reconciliation was refused' }

Write-Output '=== 5. the ledger after the reconciliation ==='
$after = (Get-FileHash -Algorithm SHA256 -LiteralPath $Ledger).Hash
Write-Output ("ledger sha256 after : " + $after)
Write-Output '--- the ledger content ---'
Get-Content -LiteralPath $Ledger -Raw -Encoding UTF8 | Write-Output
