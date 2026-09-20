param(
    [switch]$SkipBrowser
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$serverPath = Join-Path $projectRoot 'bin\ipm-server.exe'
$configPath = Join-Path $projectRoot 'config.json'
$url = 'http://127.0.0.1:8788/'

function Test-IpmHealth {
    try {
        $response = Invoke-RestMethod -UseBasicParsing -Uri ($url + 'api/health') -TimeoutSec 2
        return $response.success -eq $true -and $response.data.status -eq 'ok'
    }
    catch {
        return $false
    }
}

try {
    if (-not (Test-Path -LiteralPath $serverPath)) {
        throw "Server executable not found: $serverPath"
    }
    if (-not (Test-Path -LiteralPath $configPath)) {
        throw "Config file not found: $configPath"
    }

    if (-not (Test-IpmHealth)) {
        Start-Process -FilePath $serverPath `
            -ArgumentList @('-config', $configPath) `
            -WorkingDirectory $projectRoot `
            -WindowStyle Hidden

        $ready = $false
        for ($attempt = 0; $attempt -lt 60; $attempt++) {
            Start-Sleep -Milliseconds 250
            if (Test-IpmHealth) {
                $ready = $true
                break
            }
        }
        if (-not $ready) {
            throw 'The service did not become ready within 15 seconds.'
        }
    }

    if (-not $SkipBrowser) {
        Start-Process $url
    }
}
catch {
    Add-Type -AssemblyName PresentationFramework
    [System.Windows.MessageBox]::Show(
        $_.Exception.Message,
        'iCloud Privacy Mail startup failed',
        [System.Windows.MessageBoxButton]::OK,
        [System.Windows.MessageBoxImage]::Error
    ) | Out-Null
    exit 1
}
