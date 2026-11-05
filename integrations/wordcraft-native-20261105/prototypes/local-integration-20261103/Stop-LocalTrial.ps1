$ErrorActionPreference = 'Stop'
$trialData = Join-Path $PSScriptRoot 'runtime\trial'
$instance = Get-Content -LiteralPath (Join-Path $trialData 'integration.json') -Raw | ConvertFrom-Json
if ([IO.Path]::GetFullPath($instance.directory) -ne [IO.Path]::GetFullPath($trialData)) { throw 'Runtime directory does not match this trial.' }
if ($instance.baselineId -ne 'LD-V1-20261103-R1' -or $instance.runtimeId -notmatch '^[0-9a-f-]{36}$') { throw 'Unrecognized runtime identity.' }
Set-Content -LiteralPath (Join-Path $trialData 'stop.request') -Value $instance.runtimeId -Encoding ascii
Write-Output 'Requested this trial runtime to stop. Project data and other previews are retained.'
