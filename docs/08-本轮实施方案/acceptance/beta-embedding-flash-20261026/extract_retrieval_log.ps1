$ErrorActionPreference = 'Stop'

$LogPath = 'D:\wt\lingdoc-484-evidence\logs\server-embedding-20261026-075556.log'
$StdPath = $LogPath + '.stdout.log'
$ErrPath = $LogPath + '.stderr.log'
$Out     = 'D:\wt\lingdoc-485-evidence\embedding-run\flash-run\retrieval-log-evidence.txt'
$Copy    = 'D:\wt\lingdoc-485-evidence\embedding-run\flash-run\server-embedding-20261026-075556.log'

function Read-Shared([string]$path) {
    if (-not (Test-Path -LiteralPath $path)) { return '' }
    $stream = [System.IO.File]::Open($path, [System.IO.FileMode]::Open,
        [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
    try {
        $reader = New-Object System.IO.StreamReader($stream)
        try { return $reader.ReadToEnd() } finally { $reader.Dispose() }
    } finally { $stream.Dispose() }
}

$logText = (Read-Shared $LogPath) + "`n" + (Read-Shared $StdPath) + "`n" + (Read-Shared $ErrPath)

$lines = @()
$lines += "=== the embedding instance log (a snapshot copy) ==="
$lines += ("source: " + $LogPath)
$lines += ""

$lines += "=== 1. the embedding stage: what the application asked the provider for ==="
foreach ($m in [regex]::Matches($logText, '[^\r\n]*(?i)(OpenAIEmbedder|BatchEmbed|EmbedBatch)[^\r\n]*')) {
    $lines += ("  " + $m.Value.Trim())
}
$lines += ""

$lines += "=== 2. the retrieval: the vector channel, the keyword channel, the fusion ==="
foreach ($m in [regex]::Matches($logText, '[^\r\n]*(?i)(vectorRetrieve|Retrieval results|before fusion|after dedup|RRF rank|multi-store retrieval|query_dim)[^\r\n]*')) {
    $lines += ("  " + $m.Value.Trim())
}
$lines += ""

$lines += "=== 3. every error or warning in the log ==="
$errors = @()
foreach ($m in [regex]::Matches($logText, '[^\r\n]*(?i)(\[error\]|\[warn\]|ERROR|WARN|error:|insufficient_quota|403)[^\r\n]*')) {
    $errors += ("  " + $m.Value.Trim())
}
if ($errors.Count -eq 0) { $lines += "  (none)" } else { $lines += $errors }
$lines += ""

$lines += "=== 4. the budget lines the server logged ==="
foreach ($m in [regex]::Matches($logText, '[^\r\n]*(?i)(acceptance|budget|ledger|reserve)[^\r\n]*')) {
    $lines += ("  " + $m.Value.Trim())
}

[System.IO.File]::WriteAllLines($Out, $lines, (New-Object System.Text.UTF8Encoding($false)))
Write-Output ("written: " + $Out)

# A byte copy of the log, so the evidence survives the process.
$stream = [System.IO.File]::Open($LogPath, [System.IO.FileMode]::Open,
    [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
try {
    $target = [System.IO.File]::Create($Copy)
    try { $stream.CopyTo($target) } finally { $target.Dispose() }
} finally { $stream.Dispose() }
Write-Output ("log copy: " + $Copy + " sha256=" +
    (Get-FileHash -LiteralPath $Copy -Algorithm SHA256).Hash.ToLower())

Write-Output '--- the retrieval lines, inline ---'
$lines | Where-Object { $_ -match 'vectorRetrieve|before fusion|Retrieval results|dedup' } | ForEach-Object { Write-Output $_ }
