<#
Clears the passwords the RD app has saved for devices, so it uses the
unattended access from the server again. Run it on the PC where you use
the RD app (not on the device), as your normal user.

  .\clear-saved-password.ps1 -Id 1066522875   # one device
  .\clear-saved-password.ps1                  # every device

Why: after a successful connection the app saves the device's password
hash and tries it before the server's. When a device is reinstalled it
gets a new password, so the saved one is wrong until it is cleared.
#>
param([string]$Id = '')

$ErrorActionPreference = 'Stop'
$dir = Join-Path $env:APPDATA 'RD\config\peers'
if (-not (Test-Path -LiteralPath $dir)) {
    Write-Host "No saved devices in $dir. Nothing to do."
    return
}

# The app keeps the device list in memory and writes it back when it closes,
# so close its windows first (the RD service is left alone).
$open = @(Get-Process -Name RD -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowHandle -ne 0 })
if ($open.Count) {
    Write-Host "Closing the RD app ($($open.Count) window(s))..."
    $open | ForEach-Object { $_.CloseMainWindow() | Out-Null }
    Start-Sleep -Seconds 3
    $open | Where-Object { -not $_.HasExited } | Stop-Process -Force
}

$id = ($Id -replace '\s', '')
$files = if ($id) { @(Get-Item -LiteralPath (Join-Path $dir "$id.toml") -ErrorAction SilentlyContinue) } else { @(Get-ChildItem -LiteralPath $dir -Filter '*.toml') }
if (-not $files.Count) {
    Write-Host "Device $id has no saved settings on this PC. Nothing to do."
    return
}

$cleared = 0
foreach ($f in $files) {
    $lines = @(Get-Content -LiteralPath $f.FullName)
    # password = [ ... ] may span several lines
    $keep = New-Object System.Collections.Generic.List[string]
    $skip = $false
    foreach ($l in $lines) {
        if ($skip) { if ($l -match '\]') { $skip = $false }; continue }
        if ($l -match '^\s*password\s*=') { $skip = ($l -match '\[') -and ($l -notmatch '\]'); continue }
        $keep.Add($l)
    }
    if ($keep.Count -eq $lines.Count) { continue }
    Copy-Item -LiteralPath $f.FullName -Destination "$($f.FullName).bak" -Force
    [IO.File]::WriteAllLines($f.FullName, $keep, (New-Object System.Text.UTF8Encoding $false))  # no BOM: the app's TOML reader rejects it
    Write-Host "Cleared the saved password for $($f.BaseName)"
    $cleared++
}
if ($cleared) {
    Write-Host "Done: $cleared device(s). Start RD and connect again. A .bak copy of each changed file is next to it."
} else {
    Write-Host "No saved passwords found. If it still asks, the device has not reported its new password: restart the RD service on the device."
}
