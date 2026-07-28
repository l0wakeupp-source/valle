[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet("full", "part")]
    [string]$Mode,
    [string]$InstallDirectory = "$env:LOCALAPPDATA\Rick\bin"
)

$ErrorActionPreference = "Stop"
$target = if ($env:RICK_TARGET) { $env:RICK_TARGET } else { Join-Path $InstallDirectory "rick.exe" }
if ($Mode -eq "full") {
    $globalDir = if ($env:RICK_HOME) { $env:RICK_HOME } else { Join-Path $env:APPDATA "rick" }
    $dataDir = if ($env:RICK_DATA) { $env:RICK_DATA } else { Join-Path $env:LOCALAPPDATA "rick" }
    Remove-Item -Recurse -Force $globalDir -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $dataDir -ErrorAction SilentlyContinue
    Write-Host "Removed Rick configuration, credentials, sessions, and data."
}

if (Test-Path $target) {
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("rick-uninstall-{0}.ps1" -f ([guid]::NewGuid()))
    @"
Start-Sleep -Milliseconds 800
Remove-Item -Force '$target' -ErrorAction SilentlyContinue
Remove-Item -Force '$tmp' -ErrorAction SilentlyContinue
"@ | Set-Content -Encoding UTF8 $tmp
    $powershell = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
    if (-not (Test-Path $powershell)) { throw "Windows PowerShell was not found at $powershell" }
    Start-Process $powershell -WindowStyle Hidden -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $tmp) | Out-Null
    Write-Host "Rick executable removal scheduled as this process exits."
}
else {
    Write-Host "Rick executable not found at $target"
}
Write-Host "Uninstall complete ($Mode removal)."
