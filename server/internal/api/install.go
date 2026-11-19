package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/zeonos/rd/server/internal/store"
)

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{0,128}$`)

// psQuote makes s a single-quoted PowerShell string.
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

const installScript = `# RD installer: downloads RD build %[1]d from %[2]s, checks it and installs it.
#   irm "https://%[2]s/install?token=<token>" | iex
#   & ([scriptblock]::Create((irm https://%[2]s/install))) -Token <token> [-Group 'Site/Room'] [-WebSocket]
param(
    [string]$Token = %[3]s,
    [string]$Group = %[4]s,
    [switch]$WebSocket = $%[5]t
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host 'Run PowerShell as Administrator and try again.' -ForegroundColor Red
    return
}
if (-not $Token) {
    Write-Host 'No enrolment token. Create one in the RD console (Enrolment), then run:' -ForegroundColor Red
    Write-Host '  irm "https://%[2]s/install?token=<token>" | iex'
    return
}
$rdDir = Join-Path $env:TEMP 'rd-install-%[1]d'
Remove-Item $rdDir -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $rdDir | Out-Null
$rdZip = Join-Path $rdDir 'rd-windows-x64.zip'
Write-Host 'Downloading RD build %[1]d (%[6]d MB)...'
Invoke-WebRequest -UseBasicParsing -Uri 'https://%[2]s/api/releases/%[1]d.zip' -OutFile $rdZip
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $rdZip).Hash -ne '%[7]s') {
    Write-Host 'The download is damaged (SHA-256 mismatch). Try again.' -ForegroundColor Red
    return
}
Expand-Archive -LiteralPath $rdZip -DestinationPath $rdDir -Force
$rdInstaller = Get-ChildItem -Path $rdDir -Recurse -Filter install.ps1 | Select-Object -First 1
$rdArgs = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $rdInstaller.FullName, '-Token', $Token)
if ($Group) { $rdArgs += @('-Group', $Group) }
if ($WebSocket) { $rdArgs += '-WebSocket' }
& powershell.exe @rdArgs
$rdExit = $LASTEXITCODE
Set-Location $env:TEMP
Remove-Item $rdDir -Recurse -Force -ErrorAction SilentlyContinue
if ($rdExit -ne 0) { Write-Host "Install failed (exit code $rdExit). Log: $env:ProgramData\RD\install.log" -ForegroundColor Red }
`

const noReleaseScript = `Write-Host 'No RD client build has been uploaded yet: upload rd-windows-x64.zip in the RD console (Updates) first.' -ForegroundColor Red
`

// handleInstallScript serves the one-line installer: the newest release (the
// one being rolled out, else the highest build), with the token, group and
// WebSocket choice from the query string as parameter defaults.
func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	q := r.URL.Query()
	token := q.Get("token")
	if !tokenRe.MatchString(token) {
		http.Error(w, "Write-Host 'That is not an enrolment token.' -ForegroundColor Red", http.StatusBadRequest)
		return
	}
	rels, err := s.store.ListReleases(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	var rel *store.Release
	for i := range rels {
		if rels[i].Current {
			rel = &rels[i]
			break
		}
	}
	if rel == nil && len(rels) > 0 {
		rel = &rels[0] // highest build
	}
	if rel == nil {
		w.Write([]byte(noReleaseScript))
		return
	}
	ws := q.Get("websocket") == "1" || q.Get("ws") == "1"
	fmt.Fprintf(w, installScript, rel.Build, r.Host, psQuote(token), psQuote(q.Get("group")), ws,
		(rel.Size+500_000)/1_000_000, strings.ToUpper(rel.SHA256))
}
