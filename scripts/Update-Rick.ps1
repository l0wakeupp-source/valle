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
    $replace = @"
`$deadline = (Get-Date).AddSeconds(30)
do {
    try {
        Move-Item -LiteralPath '$tmp' -Destination '$target' -Force -ErrorAction Stop
        break
    }
    catch {
        Start-Sleep -Milliseconds 500
    }
} while ((Get-Date) -lt `$deadline)
if (-not (Test-Path -LiteralPath '$target')) { throw "Rick update could not replace $target" }
Start-Process -FilePath '$target'
"@
    Start-Process $powershell -WindowStyle Hidden -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', $replace) | Out-Null
    Write-Host "Rick update downloaded. It will finish as this process exits."
}
catch {
    Remove-Item -Force $tmp -ErrorAction SilentlyContinue
    throw
}
