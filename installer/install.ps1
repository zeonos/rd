<#
.SYNOPSIS
  Silent, unattended install of the RD client on Windows Server 2022 / Windows 11.

.DESCRIPTION
  1. Installs the client as a Windows service (starts at boot, before logon).
  2. Sets a random unattended-access password that nobody sees; the client
     reports only its hash to the RD server, which hands it to logged-in admins.
  3. Enrols the device on the server with an enrolment token, in a location group.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File install.ps1 -Token rde_xxx -Group "Office/Rack 1"

.EXAMPLE
  # Token already bound to a group (rd-api token create --group ...): no -Group needed
  powershell -ExecutionPolicy Bypass -File install.ps1 -Token rde_xxx
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Token,
    [string]$Group = "",
    [string]$Name = $env:COMPUTERNAME
)

$ErrorActionPreference = "Stop"
$logDir = Join-Path $env:ProgramData "RD"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
Start-Transcript -Path (Join-Path $logDir "install.log") -Append | Out-Null

function Fail($msg) {
    Write-Error $msg
    Stop-Transcript | Out-Null
    exit 1
}

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail "Run this script as Administrator (or as SYSTEM from a deployment tool)."
}

# --- 1. install ---------------------------------------------------------------
$src = Get-ChildItem -Path (Join-Path $PSScriptRoot "rd") -Filter *.exe -File |
    Where-Object { $_.Name -match '^(rustdesk|rd)\.exe$' } | Select-Object -First 1
if (-not $src) { Fail "Client executable not found next to this script (expected .\rd\rustdesk.exe)." }

Write-Host "Installing from $($src.FullName)"
$p = Start-Process -FilePath $src.FullName -ArgumentList "--silent-install" -Wait -PassThru
Write-Host "Installer exit code: $($p.ExitCode)"

# Locate the installed copy (install dir is named after the app: "RD").
$exe = $null
for ($i = 0; $i -lt 60 -and -not $exe; $i++) {
    # Only our own install folder: a stock RustDesk next to RD must never be touched.
    foreach ($dir in @((Join-Path $env:ProgramFiles "RD"))) {
        if (Test-Path $dir) {
            $exe = Get-ChildItem -Path $dir -Filter *.exe -File |
                Where-Object { $_.Name -ieq 'RD.exe' } | Select-Object -First 1
            if ($exe) { break }
        }
    }
    if (-not $exe) { Start-Sleep -Seconds 2 }
}
if (-not $exe) { Fail "Installed client not found under $env:ProgramFiles\RD." }
$exe = $exe.FullName
Write-Host "Installed: $exe"

# Wait for the service to answer.
$id = ""
for ($i = 0; $i -lt 60; $i++) {
    $id = (& $exe --get-id 2>$null | Out-String).Trim()
    if ($id -match '^\d{6,}$') { break }
    Start-Sleep -Seconds 2
}
if ($id -notmatch '^\d{6,}$') { Fail "The service did not start (no ID after 2 minutes)." }
Write-Host "Device ID: $id"

# --- 2. unattended password ---------------------------------------------------
$bytes = New-Object byte[] 24
[System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
$password = [Convert]::ToBase64String($bytes).Replace('+', 'A').Replace('/', 'b').TrimEnd('=')
$out = (& $exe --password $password 2>&1 | Out-String).Trim()
Remove-Variable password
if ($out -notmatch 'Done') { Fail "Setting the unattended password failed: $out" }
Write-Host "Unattended password set (random, not stored anywhere)."

# --- 3. enrol -----------------------------------------------------------------
$assign = @("--assign", "--token", $Token, "--device_name", $Name)
if ($Group) { $assign += @("--device_group_name", $Group) }
$out = (& $exe @assign 2>&1 | Out-String).Trim()
if ($out -notmatch 'Done') { Fail "Enrolment failed: $out" }
Write-Host "Enrolled on the server$(if ($Group) { " in '$Group'" })."

Write-Host ""
Write-Host "Done. Device $id ($Name) should show as online in the RD viewer within a minute."
Stop-Transcript | Out-Null
exit 0
