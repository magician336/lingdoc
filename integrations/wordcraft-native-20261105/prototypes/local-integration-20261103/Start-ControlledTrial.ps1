$ErrorActionPreference = 'Stop'
$trialRoot = $PSScriptRoot
$trialData = Join-Path $trialRoot 'runtime\controlled-trial'
$trialPort = 7211
$nativeRoot = [IO.Path]::GetFullPath((Join-Path $trialRoot '..\..\candidates\lingdoc-native-workspace-20261027'))
$configFile = Join-Path $nativeRoot 'runtime\ai-private\local-ai.json'
if (-not (Test-Path -LiteralPath $configFile)) { throw 'Existing local AI configuration is unavailable.' }
$env:LINGDOC_LOCAL_AI_CONFIG = $configFile
$env:LINGDOC_RESEARCH_GO_BINARY = '/tmp/lingdoc-local-source-ai'
New-Item -ItemType Directory -Path $trialData -Force | Out-Null
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$launchLog = Join-Path $trialData "launch-$stamp.log"
$errorLog = Join-Path $trialData "launch-$stamp.error.log"
$trialArguments = '"{0}" --port {1} --directory "{2}"' -f (Join-Path $trialRoot 'server.mjs'), $trialPort, $trialData
$trialProcess = Start-Process -FilePath (Get-Command node).Source -ArgumentList $trialArguments -WindowStyle Hidden -RedirectStandardOutput $launchLog -RedirectStandardError $errorLog -PassThru
Write-Output "Started controlled-writing process $($trialProcess.Id). Entry http://127.0.0.1:$trialPort/; log: $launchLog"
