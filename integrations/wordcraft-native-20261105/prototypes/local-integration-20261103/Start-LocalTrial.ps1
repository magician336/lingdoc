$ErrorActionPreference = 'Stop'
$trialRoot = $PSScriptRoot
$trialData = Join-Path $trialRoot 'runtime\trial'
$trialPort = 7210
try {
    $health = Invoke-RestMethod -Uri "http://127.0.0.1:$trialPort/integration/health" -TimeoutSec 3
    if ($health.baselineId -eq 'LD-V1-20261103-R1' -and $health.ready) {
        Write-Output "LingDoc is ready: http://127.0.0.1:$trialPort/"
        exit 0
    }
    throw 'This port is occupied by another application.'
} catch [System.Net.WebException] { }
catch [System.Net.Http.HttpRequestException] { }
New-Item -ItemType Directory -Path $trialData -Force | Out-Null
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$launchLog = Join-Path $trialData "launch-$stamp.log"
$errorLog = Join-Path $trialData "launch-$stamp.error.log"
$serverPath = Join-Path $trialRoot 'server.mjs'
$trialArguments = '"{0}" --port {1} --directory "{2}"' -f $serverPath, $trialPort, $trialData
$trialProcess = Start-Process -FilePath (Get-Command node).Source -ArgumentList $trialArguments -WindowStyle Hidden -RedirectStandardOutput $launchLog -RedirectStandardError $errorLog -PassThru
Write-Output "Started owned process $($trialProcess.Id). Preview will be available at http://127.0.0.1:$trialPort/; startup logs: $launchLog"
