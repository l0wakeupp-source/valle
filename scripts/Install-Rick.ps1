[CmdletBinding()]
param(
    [string]$Repository = "rick-cli/rick",
    [string]$InstallDirectory = "$env:LOCALAPPDATA\Rick\bin"
)

$ErrorActionPreference = "Stop"
$asset = "rick-windows-amd64.exe"
$release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repository/releases/latest" -Headers @{ Accept = "application/vnd.github+json" }
$download = $release.assets | Where-Object { $_.name -eq $asset } | Select-Object -First 1
if (-not $download) {
    throw "Release asset '$asset' was not found in $Repository."
}

New-Item -ItemType Directory -Force -Path $InstallDirectory | Out-Null
$target = Join-Path $InstallDirectory "rick.exe"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("rick-{0}.exe" -f ([guid]::NewGuid()))
try {
    Invoke-WebRequest -Uri $download.browser_download_url -OutFile $tmp
    Move-Item -Force $tmp $target
}
finally {
    Remove-Item -Force $tmp -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$entries = @($userPath -split ";" | Where-Object { $_ })
if ($entries -notcontains $InstallDirectory) {
    [Environment]::SetEnvironmentVariable("Path", (($entries + $InstallDirectory) -join ";"), "User")
}
$env:Path = "$InstallDirectory;$env:Path"

Write-Host "Installed Rick to $target"
Write-Host "Run: rick version"
Write-Host "If another terminal was already open, open a new terminal or run: `$env:Path = `"$InstallDirectory;`$env:Path`""
