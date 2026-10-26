$ErrorActionPreference = 'Stop'
$Base = 'D:\wt\lingdoc-484-evidence\logs\server-embedding-20261026-075556.log'
$Out  = 'D:\wt\lingdoc-485-evidence\embedding-run\flash-run'

function Copy-Shared([string]$src, [string]$dst) {
    if (-not (Test-Path -LiteralPath $src)) { Write-Output ("absent: " + $src); return }
    $stream = [System.IO.File]::Open($src, [System.IO.FileMode]::Open,
        [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
    try {
        $target = [System.IO.File]::Create($dst)
        try { $stream.CopyTo($target) } finally { $target.Dispose() }
    } finally { $stream.Dispose() }
    Write-Output ((Split-Path -Leaf $dst) + " bytes=" + (Get-Item -LiteralPath $dst).Length +
                  " sha256=" + (Get-FileHash -LiteralPath $dst -Algorithm SHA256).Hash.ToLower())
}

Copy-Shared ($Base + '.stdout.log') (Join-Path $Out 'server-embedding-20261026-075556.stdout.log')
Copy-Shared ($Base + '.stderr.log') (Join-Path $Out 'server-embedding-20261026-075556.stderr.log')
