[CmdletBinding()]
param(
    [string]$Repository = "rick-cli/rick",
    [string]$InstallDirectory = "$env:LOCALAPPDATA\Rick\bin"
)

$ErrorActionPreference = "Stop"
$asset = "rick-windows-amd64.exe"
$target = if ($env:RICK_TARGET) { $env:RICK_TARGET } else { Join-Path $InstallDirectory "rick.exe" }
$InstallDirectory = Split-Path -Parent $target
$download = "https://github.com/$Repository/releases/latest/download/$asset"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("rick-update-{0}.exe" -f ([guid]::NewGuid()))
New-Item -ItemType Directory -Force -Path $InstallDirectory | Out-Null
try {
    Invoke-WebRequest -Uri $download -OutFile $tmp
    if (Test-Path $target) {
        $current = (Get-FileHash -Algorithm SHA256 $target).Hash
        $next = (Get-FileHash -Algorithm SHA256 $tmp).Hash
        if ($current -eq $next) {
            Remove-Item -Force $tmp
            Write-Host "Rick is already up to date."
            exit 0
        }
    }
    $powershell = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
    if (-not (Test-Path $powershell)) { throw "Windows PowerShell was not found at $powershell" }
    $replace = "Start-Sleep -Milliseconds 800; Move-Item -Force '$tmp' '$target'; & '$target' version"
    Start-Process $powershell -WindowStyle Hidden -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', $replace) | Out-Null
    Write-Host "Rick update downloaded. It will finish as this process exits."
}
catch {
    Remove-Item -Force $tmp -ErrorAction SilentlyContinue
    throw
}
