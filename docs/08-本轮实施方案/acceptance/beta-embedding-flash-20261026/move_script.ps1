$ErrorActionPreference = 'Stop'
$src = 'D:\wt\lingdoc-485-evidence\embedding-run\flash-run\_scratch_header.ps1'
$dst = 'D:\wt\lingdoc-485-evidence\embedding-run\flash-run\build_and_start_embedding.ps1'
if (Test-Path -LiteralPath $src) {
    Move-Item -LiteralPath $src -Destination $dst -Force
    Write-Output ("moved: " + $src + " -> " + $dst)
} else {
    Write-Output ("nothing to move: " + $src)
}
