$ErrorActionPreference = 'Stop'

$LogPath = 'D:\wt\lingdoc-484-evidence\logs\server-embedding-20261026-075556.log'
$StdPath = $LogPath + '.stdout.log'
$ErrPath = $LogPath + '.stderr.log'
$Out     = 'D:\wt\lingdoc-485-evidence\embedding-run\flash-run\startup-log-queue-identity.txt'

function Read-Shared([string]$path) {
    if (-not (Test-Path -LiteralPath $path)) { return '' }
    $stream = [System.IO.File]::Open($path, [System.IO.FileMode]::Open,
        [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
    try {
        $reader = New-Object System.IO.StreamReader($stream)
        try { return $reader.ReadToEnd() } finally { $reader.Dispose() }
    } finally { $stream.Dispose() }
}

$logText  = (Read-Shared $LogPath) + "`n" + (Read-Shared $StdPath) + "`n" + (Read-Shared $ErrPath)

Write-Output ("log bytes read: " + $logText.Length)

$queueLines = [regex]::Matches($logText, '[^\r\n]*queue_redis_db=[^\r\n]*')
Write-Output ("=== queue identity lines: " + $queueLines.Count + " ===")
foreach ($m in $queueLines) { Write-Output ($m.Value.Trim()) }

$source = [regex]::Matches($logText, 'queue_db_source=([a-z\-]+)')
$kinds = @{}
foreach ($s in $source) { $kinds[$s.Groups[1].Value] = $true }
Write-Output ("queue_db_source values: " + (($kinds.Keys | Sort-Object) -join ', '))

Write-Output '=== the ledger state after the restart ==='
foreach ($lp in @(
    'D:\wt\lingdoc-484-evidence\ledger\qwen-embedding-budget.json.calls.json',
    'D:\wt\lingdoc-484-evidence\ledger\qwen-beta-sqlite.json',
    'D:\wt\lingdoc-484-evidence\ledger\qwen-beta-sqlite.json.calls.json')) {
    if (Test-Path -LiteralPath $lp) {
        $sha = (Get-FileHash -Algorithm SHA256 -LiteralPath $lp).Hash.ToLower()
        $doc = [System.IO.File]::ReadAllText($lp) | ConvertFrom-Json
        Write-Output ((Split-Path -Leaf $lp) + " sha256=" + $sha +
                      " requests=" + @($doc.requests).Count +
                      " blocked=" + [string]$doc.blocked +
                      " reserved_tokens=" + [string]$doc.reserved_tokens +
                      " reserved_nano_cny=" + [string]$doc.reserved_nano_cny +
                      " actual_nano_cny=" + [string]$doc.actual_nano_cny +
                      " profile=" + [string]$doc.profile)
        foreach ($r in @($doc.requests)) {
            Write-Output ("    request " + $r.number + " model=" + $r.model + " status=" + $r.status +
                          " bound=" + $r.input_upper_bound_tokens + " in=" + $r.input_tokens +
                          " usage=" + $r.usage_status +
                          " reserved_cny=" + $r.reserved_nano_cny + " actual_cny=" + $r.actual_nano_cny +
                          " reconciled=" + [string]($null -ne $r.reconciliation))
        }
    } else {
        Write-Output ((Split-Path -Leaf $lp) + " ABSENT")
    }
}

$lines = @()
$lines += "=== the startup log of the embedding instance (queue identity) ==="
$lines += ("log: " + $LogPath)
$lines += ("queue identity lines: " + $queueLines.Count)
foreach ($m in $queueLines) { $lines += ("  " + $m.Value.Trim()) }
$lines += ("queue_db_source values: " + (($kinds.Keys | Sort-Object) -join ', '))
$lines += ""
$lines += "=== every line that mentions the queue or the worker pools ==="
foreach ($m in [regex]::Matches($logText, '[^\r\n]*(?i)(asynq|queue|worker pool)[^\r\n]*')) {
    $lines += ("  " + $m.Value.Trim())
}
[System.IO.File]::WriteAllLines($Out, $lines, (New-Object System.Text.UTF8Encoding($false)))
Write-Output ("written: " + $Out)
