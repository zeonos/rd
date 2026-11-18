<#
.SYNOPSIS
  Checks that this machine can reach the RD server through its firewall.
  Run it at each site before installing (phase 0 firewall test).

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File test-connection.ps1
  powershell -ExecutionPolicy Bypass -File test-connection.ps1 -Server rd.sfohosting.dk
#>
param([string]$Server = "rd.sfohosting.dk")

$results = [ordered]@{}
function Report($name, $ok, $detail) {
    $results[$name] = $ok
    $mark = if ($ok -eq $true) { "PASS" } elseif ($ok -eq $false) { "FAIL" } else { "WARN" }
    $color = if ($ok -eq $true) { "Green" } elseif ($ok -eq $false) { "Red" } else { "Yellow" }
    $detail = ("$detail" -replace '\s+', ' ').Trim()
    Write-Host ("[{0}] {1}: {2}" -f $mark, $name, $detail) -ForegroundColor $color
}

Write-Host "RD connection test for $Server on $env:COMPUTERNAME ($(Get-Date -Format s))`n"

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

# 1. DNS
try {
    $ips = [System.Net.Dns]::GetHostAddresses($Server) | ForEach-Object { $_.IPAddressToString }
    Report "DNS" $true ($ips -join ", ")
} catch { Report "DNS" $false $_.Exception.Message }

# 2. Proxy configuration
$winhttp = (netsh winhttp show proxy | Out-String)
$ieProxy = [System.Net.WebRequest]::GetSystemWebProxy().GetProxy([Uri]"https://$Server/")
if ($winhttp -match "Direct access" -and $ieProxy.Host -eq $Server) {
    Report "Proxy" $true "none configured"
} else {
    Report "Proxy" $null "a proxy is configured ($ieProxy). Set it in the RD client (Settings > Network > Proxy) if direct 443 is blocked."
}

# 3. TCP 443
try {
    $tcp = New-Object System.Net.Sockets.TcpClient
    $task = $tcp.ConnectAsync($Server, 443)
    if ($task.Wait(5000) -and $tcp.Connected) { Report "TCP 443" $true "connected" }
    else { Report "TCP 443" $false "no connection within 5 s - outbound 443 to $Server is blocked" }
    $tcp.Close()
} catch { Report "TCP 443" $false $_.Exception.Message }

# 3b. RustDesk ports (the open-source server registers devices over UDP 21116,
#     which cannot be tested from here; TCP 21116 on the same host is a good hint)
foreach ($port in 21115, 21116, 21117) {
    try {
        $tcp = New-Object System.Net.Sockets.TcpClient
        $task = $tcp.ConnectAsync($Server, $port)
        if ($task.Wait(5000) -and $tcp.Connected) { Report "TCP $port" $true "connected" }
        else { Report "TCP $port" $false "blocked - allow outbound TCP 21115-21117 and UDP 21116 to $Server" }
        $tcp.Close()
    } catch { Report "TCP $port" $false $_.Exception.Message }
}

# 4. TLS certificate (detects TLS inspection)
try {
    $tcp = New-Object System.Net.Sockets.TcpClient($Server, 443)
    $ssl = New-Object System.Net.Security.SslStream($tcp.GetStream(), $false, { $true })
    # Windows PowerShell 5.1 may default to TLS 1.0/1.1 here; the server only speaks TLS 1.2+.
    $ssl.AuthenticateAsClient($Server, $null, [System.Security.Authentication.SslProtocols]::Tls12, $false)
    $cert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($ssl.RemoteCertificate)
    $issuer = $cert.Issuer
    $ssl.Close(); $tcp.Close()
    if ($issuer -match "Let's Encrypt|O=Let's Encrypt|ISRG|ZeroSSL") {
        Report "TLS" $true "certificate issued by $issuer"
    } else {
        Report "TLS" $false "unexpected issuer '$issuer' - the firewall is probably inspecting TLS. Exempt $Server from inspection."
    }
} catch { Report "TLS" $false $_.Exception.Message }

# 5. WebSocket upgrade on /ws/id (what the client actually uses)
try {
    $ws = New-Object System.Net.WebSockets.ClientWebSocket
    $cts = New-Object System.Threading.CancellationTokenSource(8000)
    $ws.ConnectAsync([Uri]"wss://$Server/ws/id", $cts.Token).Wait()
    Report "WebSocket" ($ws.State -eq "Open") "state $($ws.State)"
    $ws.Dispose()
} catch {
    $msg = $_.Exception.InnerException.Message
    if (-not $msg) { $msg = $_.Exception.Message }
    Report "WebSocket" $false "$msg - a proxy or firewall may block WebSocket upgrades"
}

# 6. API server
try {
    $r = Invoke-WebRequest -Uri "https://$Server/health" -UseBasicParsing -TimeoutSec 10
    Report "API" ($r.Content.Trim() -eq "ok") "HTTP $($r.StatusCode)"
} catch { Report "API" $null "not reachable yet ($($_.Exception.Message)) - fine before the API server is deployed" }

$failed = @($results.Values | Where-Object { $_ -eq $false }).Count
Write-Host ""
if ($failed -eq 0) { Write-Host "Result: this site can use RD." -ForegroundColor Green; exit 0 }
Write-Host "Result: $failed check(s) failed - see above." -ForegroundColor Red
exit 1
