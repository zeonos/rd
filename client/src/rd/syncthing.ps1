[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

# Syncthing status for RD: finds the local Syncthing, asks its REST API how
# each folder and each remote device is doing, prints one line of JSON.
# Runs as SYSTEM from the RD service every few minutes (see fleet.rs).

function Out-Report($o) { $o | ConvertTo-Json -Depth 6 -Compress; exit 0 }

# Config locations: from the command line of a running syncthing.exe
# (--home / --config), then the usual places.
$dirs = New-Object System.Collections.Generic.List[string]
try {
    foreach ($p in @(Get-CimInstance Win32_Process -Filter "Name='syncthing.exe'")) {
        $cl = [string]$p.CommandLine
        foreach ($m in [regex]::Matches($cl, '--?(?:home|config)(?:=|\s+)(?:"([^"]+)"|(\S+))')) {
            $d = if ($m.Groups[1].Success) { $m.Groups[1].Value } else { $m.Groups[2].Value }
            $dirs.Add($d.TrimEnd('\'))
        }
        try {
            $owner = Invoke-CimMethod -InputObject $p -MethodName GetOwner
            if ($owner.User) { $dirs.Add("$env:SystemDrive\Users\$($owner.User)\AppData\Local\Syncthing") }
        } catch {}
    }
} catch {}
$globs = @(
    "$env:SystemDrive\Users\*\AppData\Local\Syncthing",
    "$env:ProgramData\Syncthing",
    "$env:SystemRoot\System32\config\systemprofile\AppData\Local\Syncthing",
    "$env:SystemRoot\ServiceProfiles\LocalService\AppData\Local\Syncthing",
    "$env:SystemDrive\Users\*\AppData\Local\SyncTrayzor\syncthing",
    "$env:SystemDrive\Users\*\AppData\Roaming\SyncTrayzor\syncthing"
)
foreach ($g in $globs) {
    try { foreach ($i in @(Get-Item -Path $g -Force)) { $dirs.Add($i.FullName) } } catch {}
}
$configs = @($dirs | Select-Object -Unique | ForEach-Object { Join-Path $_ 'config.xml' } |
    Where-Object { Test-Path -LiteralPath $_ } |
    Sort-Object { (Get-Item -LiteralPath $_ -Force).LastWriteTimeUtc } -Descending)

$running = @(Get-Process -Name syncthing -ErrorAction SilentlyContinue).Count -gt 0
if ($configs.Count -eq 0) {
    if ($running) { Out-Report ([ordered]@{ found = $true; error = 'Syncthing runs, but its config.xml was not found' }) }
    Out-Report ([ordered]@{ found = $false })
}

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
# Syncthing's GUI certificate is self-signed; the API is only used on this machine.
[Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }

$lastError = ''
foreach ($cfgPath in $configs) {
    try {
        $x = [xml](Get-Content -LiteralPath $cfgPath -Raw -Encoding UTF8)
        $gui = $x.configuration.gui
        $key = [string]$gui.apikey
        if (-not $key) { $lastError = "No API key in $cfgPath"; continue }
        $addr = [string]$gui.address
        if ($addr -match '^(.*):(\d+)$') { $h = $Matches[1].Trim('[', ']'); $port = $Matches[2] } else { $h = ''; $port = '8384' }
        if ($h -eq '' -or $h -eq '0.0.0.0' -or $h -eq '::') { $h = '127.0.0.1' }
        if ($h.Contains(':')) { $h = "[$h]" }
        $scheme = if ([string]$gui.tls -eq 'true') { 'https' } else { 'http' }
        $base = "${scheme}://${h}:$port"
        $hdr = @{ 'X-API-Key' = $key }
        function Get-ST($path) { Invoke-RestMethod -Uri ($base + $path) -Headers $hdr -TimeoutSec 15 -UseBasicParsing }

        $status = Get-ST '/rest/system/status'
        $version = (Get-ST '/rest/system/version').version
        $conns = (Get-ST '/rest/system/connections').connections
        $stats = $null
        try { $stats = Get-ST '/rest/stats/device' } catch {}
        $myID = [string]$status.myID

        $devices = @()
        $myName = ''
        foreach ($d in @($x.configuration.device)) {
            if (-not $d.id) { continue }
            if ($d.id -eq $myID) { $myName = [string]$d.name; continue }
            $c = $conns.($d.id)
            $seen = ''
            if ($stats -and $stats.($d.id)) { $seen = [string]$stats.($d.id).lastSeen }
            $devices += [ordered]@{
                id = [string]$d.id; name = [string]$d.name
                connected = [bool]($c -and $c.connected); paused = ([string]$d.paused -eq 'true' -or [bool]($c -and $c.paused))
                last_seen = $seen
            }
        }

        $folders = @()
        foreach ($f in @($x.configuration.folder)) {
            if (-not $f.id) { continue }
            $fid = [uri]::EscapeDataString([string]$f.id)
            $paused = ([string]$f.paused -eq 'true')
            $row = [ordered]@{
                id = [string]$f.id; label = [string]$f.label; path = [string]$f.path; type = [string]$f.type
                state = ''; paused = $paused; need_bytes = 0; need_items = 0; global_bytes = 0; in_sync_bytes = 0; errors = 0
                devices = @()
            }
            if (-not $paused) {
                try {
                    $s = Get-ST "/rest/db/status?folder=$fid"
                    $row.state = [string]$s.state
                    $row.need_bytes = [int64]$s.needBytes
                    $row.need_items = [int64]$s.needFiles + [int64]$s.needDirectories + [int64]$s.needSymlinks + [int64]$s.needDeletes
                    $row.global_bytes = [int64]$s.globalBytes
                    $row.in_sync_bytes = [int64]$s.inSyncBytes
                    $row.errors = [int]$s.errors + [int]$s.pullErrors
                } catch { $row.state = 'error: ' + $_.Exception.Message }
                foreach ($fd in @($f.device)) {
                    if (-not $fd.id -or $fd.id -eq $myID) { continue }
                    try {
                        $c = Get-ST "/rest/db/completion?folder=$fid&device=$([uri]::EscapeDataString([string]$fd.id))"
                        $row.devices += [ordered]@{
                            id = [string]$fd.id; completion = [double]$c.completion; need_bytes = [int64]$c.needBytes
                            remote_state = [string]$c.remoteState
                        }
                    } catch {}
                }
            }
            $folders += $row
        }
        Out-Report ([ordered]@{
            found = $true; version = [string]$version; my_id = $myID; my_name = $myName
            folders = $folders; devices = $devices
        })
    } catch {
        $lastError = "$($cfgPath): $($_.Exception.Message)"
    }
}
Out-Report ([ordered]@{ found = $true; error = $lastError })
