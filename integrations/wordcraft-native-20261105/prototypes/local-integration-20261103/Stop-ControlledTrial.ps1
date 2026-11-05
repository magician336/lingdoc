$ErrorActionPreference = 'Stop'
$trialData = Join-Path $PSScriptRoot 'runtime\controlled-trial'
$infoFile = Join-Path $trialData 'integration.json'
if (-not (Test-Path -LiteralPath $infoFile)) { throw 'No owned controlled-writing runtime metadata.' }
$trialInfo = Get-Content -Raw -LiteralPath $infoFile | ConvertFrom-Json
if ([IO.Path]::GetFullPath($trialInfo.directory) -ne [IO.Path]::GetFullPath($trialData) -or $trialInfo.origin -ne 'http://127.0.0.1:7211' -or -not $trialInfo.runtimeId) { throw 'Runtime identity mismatch.' }
[IO.File]::WriteAllText((Join-Path $trialData 'stop.request'), $trialInfo.runtimeId)
Write-Output 'Requested shutdown of the matching controlled-writing runtime; project data retained.'
