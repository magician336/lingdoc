$ErrorActionPreference = 'Stop'

$WorkTree = 'D:\wt\lingdoc-beta-status'
$Probe    = Join-Path $WorkTree 'internal\models\embedding\zz_flash_chain_local_test.go'
$Evidence = 'D:\wt\lingdoc-485-evidence\embedding-run\flash-run'
$Archive  = Join-Path $Evidence 'probes'
$Transcript = Join-Path $Evidence 'local-chain-test-transcript.txt'

New-Item -ItemType Directory -Force -Path $Archive | Out-Null

$env:GOCACHE = 'D:\wt\gocache-619'
$env:CGO_CFLAGS = '-I C:\Users\ASUS\.sqlite-headers'

Push-Location $WorkTree
try {
    Write-Output '=== the probe file is gofmt-clean (no output means clean) ==='
    $fmt = gofmt -l 'internal/models/embedding/zz_flash_chain_local_test.go'
    Write-Output ("gofmt -l output: [" + $fmt + "]")

    Write-Output '=== go vet ==='
    $vet = go vet ./internal/models/embedding/ 2>&1
    Write-Output ("vet output: [" + ($vet -join ' | ') + "]")

    Write-Output '=== go test (local, no network) ==='
    $test = go test ./internal/models/embedding/ -run TestZZFlashChainLocalNoNetwork -v -count=1 -timeout 600s 2>&1
    $test | Out-File -LiteralPath $Transcript -Encoding utf8
    Write-Output ("exit code: " + $LASTEXITCODE)
    $test | Select-Object -Last 12 | ForEach-Object { Write-Output ("  " + $_) }
} finally {
    Pop-Location
}

# Move the probe out of the working tree. It is MOVED, never deleted: the
# zero-deletion rule keeps every artifact of this round.
if (Test-Path -LiteralPath $Probe) {
    Move-Item -LiteralPath $Probe -Destination (Join-Path $Archive 'zz_flash_chain_local_test.go') -Force
    Write-Output 'probe moved out of the working tree into the evidence directory'
} else {
    Write-Output 'probe already out of the working tree'
}

Write-Output '=== working tree status after the move ==='
Push-Location $WorkTree
try {
    $status = git status --porcelain
    if ([string]::IsNullOrWhiteSpace(($status -join ''))) {
        Write-Output 'working tree clean'
    } else {
        $status | ForEach-Object { Write-Output ("  " + $_) }
    }
} finally {
    Pop-Location
}

Write-Output ("transcript: " + $Transcript)
