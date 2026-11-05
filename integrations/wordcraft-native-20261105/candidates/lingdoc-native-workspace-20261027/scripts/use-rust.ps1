param([switch]$Repair)

# Dot-source this file for an interactive shell, or invoke it from build scripts.
# All installation and cache paths are relative to the candidate, never TEMP.
$ErrorActionPreference = 'Stop'
$candidateRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$rustRoot = Join-Path $candidateRoot 'build/rust'
$componentRoot = Join-Path $candidateRoot 'build/toolchain'
$requiredVersion = '1.95.0'
$hostTarget = 'x86_64-pc-windows-msvc'
$wasmTarget = 'wasm32-unknown-unknown'

if ($Repair) {
    $running = @(Get-Process cargo, rustc, rustdoc, rustfmt, clippy-driver -ErrorAction SilentlyContinue)
    if ($running.Count) { throw 'Rust tools are running; finish that operation before repairing the local toolchain.' }
    $versionFile = Join-Path $componentRoot 'version'
    if (-not (Test-Path -LiteralPath $versionFile -PathType Leaf) -or
        (Get-Content -LiteralPath $versionFile -Raw).Trim() -notmatch '^1\.95\.0(?:\s|$)') {
        throw "No extracted Rust $requiredVersion components at $componentRoot. Reuse the local archives first; this script does not download or upgrade Rust."
    }
    # Distribution archives contain component/bin/... rather than a ready bin/.
    # Copy missing files only; never replace the existing compiler or clear caches.
    $components = @('rustc', 'cargo', "rust-std-$hostTarget", "rust-std-$wasmTarget", 'rustfmt-preview', 'clippy-preview')
    $copied = 0
    foreach ($component in $components) {
        $sourceRoot = Join-Path $componentRoot $component
        if (-not (Test-Path -LiteralPath $sourceRoot -PathType Container)) { continue }
        foreach ($directory in @('bin', 'lib', 'libexec', 'etc', 'share')) {
            $sourceDirectory = Join-Path $sourceRoot $directory
            if (-not (Test-Path -LiteralPath $sourceDirectory -PathType Container)) { continue }
            foreach ($file in Get-ChildItem -LiteralPath $sourceDirectory -File -Recurse) {
                $relative = $file.FullName.Substring($sourceRoot.Length).TrimStart([char[]]'\/')
                $destination = Join-Path $rustRoot $relative
                if (Test-Path -LiteralPath $destination) { continue }
                New-Item -ItemType Directory -Force -Path (Split-Path -Parent $destination) | Out-Null
                Copy-Item -LiteralPath $file.FullName -Destination $destination
                $copied++
            }
        }
    }
    Write-Host "Restored $copied missing files from local components. Existing files and Cargo caches were preserved."
}

$repairHint = "Run scripts/use-rust.ps1 -Repair to restore missing files from build/toolchain; do not redownload the full bundle or clear build/target."
foreach ($name in @('cargo', 'rustc', 'rustdoc')) {
    if (-not (Test-Path -LiteralPath (Join-Path $rustRoot "bin/$name.exe") -PathType Leaf)) {
        throw "Missing $rustRoot/bin/$name.exe. $repairHint"
    }
}

$rustBin = Join-Path $rustRoot 'bin'
if (($env:PATH -split ';') -notcontains $rustBin) { $env:PATH = "$rustBin;$env:PATH" }
$compilerVersion = (& "$rustBin/rustc.exe" -vV | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $compilerVersion -notmatch '(?m)^release: 1\.95\.0\r?$' -or
    $compilerVersion -notmatch '(?m)^host: x86_64-pc-windows-msvc\r?$') {
    throw "Expected Rust $requiredVersion for $hostTarget at $rustRoot; found: $compilerVersion"
}
$cargoVersion = (& "$rustBin/cargo.exe" --version | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $cargoVersion -notmatch '^cargo 1\.95\.0\s') { throw "Unexpected Cargo: $cargoVersion" }
$rustdocVersion = (& "$rustBin/rustdoc.exe" --version | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $rustdocVersion -notmatch '^rustdoc 1\.95\.0\s') { throw "Unexpected rustdoc: $rustdocVersion" }
foreach ($target in @($hostTarget, $wasmTarget)) {
    $libraryDirectory = Join-Path $rustRoot "lib/rustlib/$target/lib"
    foreach ($library in @('libcore-*.rlib', 'libstd-*.rlib')) {
        if (-not (Get-ChildItem -LiteralPath $libraryDirectory -Filter $library -File -ErrorAction SilentlyContinue)) {
            throw "Missing $library for $target in $libraryDirectory. $repairHint"
        }
    }
}
$bindgen = Join-Path $candidateRoot 'tools/wasm-bindgen.exe'
if (-not (Test-Path -LiteralPath $bindgen -PathType Leaf)) { throw "Missing wasm-bindgen: $bindgen" }
$bindgenVersion = (& $bindgen --version | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $bindgenVersion -ne 'wasm-bindgen 0.2.129') { throw "Unexpected wasm-bindgen: $bindgenVersion" }

$vcvars = 'C:\Program Files\Microsoft Visual Studio\2022\Community\VC\Auxiliary\Build\vcvars64.bat'
if (-not (Test-Path -LiteralPath $vcvars -PathType Leaf)) { throw "Missing VS2022 C++ build environment: $vcvars" }
$nativeEnvironment = & cmd.exe /d /c "call `"$vcvars`" >nul && set"
if ($LASTEXITCODE -ne 0) { throw 'VS2022 build environment unavailable' }
foreach ($line in $nativeEnvironment) {
    if ($line -match '^([^=]+)=(.*)$') { [Environment]::SetEnvironmentVariable($matches[1], $matches[2], 'Process') }
}
$env:RUSTC = Join-Path $rustBin 'rustc.exe'
$env:RUSTDOC = Join-Path $rustBin 'rustdoc.exe'
$env:CARGO_HOME = Join-Path $candidateRoot 'build/cargo-home'
# Cargo searches CARGO_HOME/bin before PATH for subcommands. Existing rustup
# proxies must resolve to this same offline toolchain, not a global default.
$env:RUSTUP_TOOLCHAIN = $rustRoot
$env:RUSTUP_HOME = Join-Path $candidateRoot 'build/rustup-home'
$env:CARGO_TARGET_DIR = Join-Path $candidateRoot 'build/target'
$env:TEMP = Join-Path $candidateRoot 'build/tmp'
$env:TMP = $env:TEMP
New-Item -ItemType Directory -Force -Path $env:TEMP | Out-Null

$optionalMissing = @('rustfmt.exe', 'cargo-fmt.exe', 'cargo-clippy.exe', 'clippy-driver.exe') |
    Where-Object { -not (Test-Path -LiteralPath (Join-Path $rustBin $_) -PathType Leaf) }
[pscustomobject]@{
    rustRoot = $rustRoot
    cargo = Join-Path $rustBin 'cargo.exe'
    rustc = $env:RUSTC
    rustdoc = $env:RUSTDOC
    rustVersion = $requiredVersion
    cargoVersion = $cargoVersion
    bindgenVersion = $bindgenVersion
    hostTarget = $hostTarget
    wasmTarget = $wasmTarget
    cargoHome = $env:CARGO_HOME
    rustupToolchain = $env:RUSTUP_TOOLCHAIN
    target = $env:CARGO_TARGET_DIR
    optionalMissing = @($optionalMissing)
}
