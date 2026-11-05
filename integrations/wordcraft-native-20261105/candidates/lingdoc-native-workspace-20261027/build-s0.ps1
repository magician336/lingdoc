param(
    [string]$RunName = '',
    [int]$Jobs = 2,
    [switch]$CheckEnvironment
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$toolchain = & (Join-Path $root 'scripts/use-rust.ps1')
if ($CheckEnvironment) { $toolchain | ConvertTo-Json -Depth 4; exit 0 }
$rust = $toolchain.rustRoot
$source = Join-Path $root 'engine'
$build = Join-Path $root 'build'
$logRoot = Join-Path $build 'logs'
$outDir = Join-Path $build 'out'
$bindgen = Join-Path $root 'tools/wasm-bindgen.exe'

$env:CARGO_PROFILE_RELEASE_LTO = 'false'
$env:CARGO_PROFILE_RELEASE_CODEGEN_UNITS = '16'
$env:RUSTFLAGS = ''
$env:CARGO_INCREMENTAL = '0'
$env:CARGO_NET_GIT_FETCH_WITH_CLI = 'true'
New-Item -ItemType Directory -Force -Path $logRoot, $outDir, $env:TEMP | Out-Null

$other = Get-Process cargo, rustc -ErrorAction SilentlyContinue | Where-Object { $_.Id -ne $PID }
if ($other) {
    $names = ($other | ForEach-Object { "$($_.ProcessName):$($_.Id)" }) -join ', '
    throw "another cargo/rustc is running ($names); refusing to start a second release build"
}

if (-not $RunName) { $RunName = 's0-' + (Get-Date -Format 'yyyyMMdd-HHmmss') }
if (Test-Path -LiteralPath (Join-Path $logRoot "$RunName-result.json")) { throw "RunName '$RunName' already has a result" }

# Match the fixed R11 build: its host-mode patch requires an offline lockfile re-resolution.
$arguments = @('build', '--target', 'wasm32-unknown-unknown', '-p', 'wordcraft-web', '--release', '--jobs', "$Jobs", '--offline')
$record = [ordered]@{
    startedUtc   = [DateTime]::UtcNow.ToString('o')
    mode         = 'native-lingdoc-s0'
    arguments    = $arguments
    source       = $source
    rustc        = $env:RUSTC
    rustdoc      = $env:RUSTDOC
    toolchain    = $toolchain
    cargoHome    = $env:CARGO_HOME
    target       = $env:CARGO_TARGET_DIR
    lto          = $env:CARGO_PROFILE_RELEASE_LTO
    codegenUnits = $env:CARGO_PROFILE_RELEASE_CODEGEN_UNITS
    rustflags    = $env:RUSTFLAGS
    incremental  = $env:CARGO_INCREMENTAL
    pid          = $PID
}
$json = $record | ConvertTo-Json -Depth 6
[IO.File]::WriteAllText((Join-Path $logRoot "$RunName-command.json"), $json, (New-Object Text.UTF8Encoding($false)))
Copy-Item -LiteralPath (Join-Path $source 'Cargo.lock') -Destination (Join-Path $logRoot "$RunName-Cargo.lock.before")

Push-Location -LiteralPath $source
try {
    $ErrorActionPreference = 'Continue'
    & "$rust/bin/cargo.exe" @arguments *> (Join-Path $logRoot "$RunName.log")
    $code = $LASTEXITCODE
    $record['cargoExit'] = $code
    Copy-Item -LiteralPath (Join-Path $source 'Cargo.lock') -Destination (Join-Path $logRoot "$RunName-Cargo.lock.after")
    if ($code -eq 0) {
        & $bindgen "$env:CARGO_TARGET_DIR/wasm32-unknown-unknown/release/wordcraft-web.wasm" --target web --no-typescript --out-name wordcraft --out-dir $outDir *> (Join-Path $logRoot "$RunName-bindgen.log")
        $code = $LASTEXITCODE
        $record['bindgenExit'] = $code
    }
    $record['finishedUtc'] = [DateTime]::UtcNow.ToString('o')
    $record['exitCode'] = $code
    $recordJson = $record | ConvertTo-Json -Depth 6
    [IO.File]::WriteAllText((Join-Path $logRoot "$RunName-result.json"), $recordJson, (New-Object Text.UTF8Encoding($false)))
    Get-Content -LiteralPath (Join-Path $logRoot "$RunName.log") -Tail 30
    exit $code
} finally {
    Pop-Location
}
