[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$ErrorActionPreference = 'SilentlyContinue'
$ProgressPreference = 'SilentlyContinue'

$cs = Get-CimInstance Win32_ComputerSystem
$bios = Get-CimInstance Win32_BIOS
$os = Get-CimInstance Win32_OperatingSystem
$ubr = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion').UBR

$disks = @(Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3' | ForEach-Object {
    [ordered]@{ name = $_.DeviceID; label = [string]$_.VolumeName; size = [int64]$_.Size; free = [int64]$_.FreeSpace }
})

$keys = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*',
        'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'
$software = @(Get-ItemProperty $keys |
    Where-Object { $_.DisplayName -and -not $_.SystemComponent -and -not $_.ParentKeyName } |
    ForEach-Object { [pscustomobject]@{ name = [string]$_.DisplayName; version = [string]$_.DisplayVersion; publisher = [string]$_.Publisher } } |
    Sort-Object name, version -Unique)

# Offline search: uses the result of Windows Update's last scan, no network.
$pending = -1
try {
    $searcher = (New-Object -ComObject Microsoft.Update.Session).CreateUpdateSearcher()
    $searcher.Online = $false
    $pending = $searcher.Search('IsInstalled=0 and IsHidden=0').Updates.Count
} catch {}

$rebootPending = (Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending') -or
    (Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired') -or
    [bool](Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager').PendingFileRenameOperations

[ordered]@{
    manufacturer    = [string]$cs.Manufacturer
    model           = [string]$cs.Model
    serial          = [string]$bios.SerialNumber
    memory          = [int64]$cs.TotalPhysicalMemory
    os_caption      = [string]$os.Caption
    os_build        = if ($ubr) { "$($os.BuildNumber).$ubr" } else { [string]$os.BuildNumber }
    boot_time       = if ($os.LastBootUpTime) { $os.LastBootUpTime.ToUniversalTime().ToString('o') } else { '' }
    pending_updates = $pending
    reboot_pending  = $rebootPending
    disks           = $disks
    software        = $software
} | ConvertTo-Json -Depth 4 -Compress
