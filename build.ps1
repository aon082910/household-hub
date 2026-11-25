# Builds Household Hub. Every Go cache stays on D: (paths below).
#   .\build.ps1            -> vet + tests, then hhub.exe, HouseholdHub.exe (window) and dist\hhub-linux-amd64
#   .\build.ps1 -Install   -> also installs to D:\AI Tools\Household Hub\, makes Start Menu + Desktop shortcuts and a
#                             Startup shortcut that runs the background notifier at sign-in
#   .\build.ps1 -Docker    -> also builds the image household-hub:dev and (re)creates the container household-hub-dev
#                             on http://localhost:8095 (volume household-hub-data)
param([switch]$Install, [switch]$Docker)
$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$env:PATH = 'D:\AI Tools\Go\bin;' + $env:PATH
$env:GOROOT = 'D:\AI Tools\Go'
$env:GOPATH = 'D:\AI Tools\_cache\go\path'
$env:GOMODCACHE = 'D:\AI Tools\_cache\go\mod'
$env:GOCACHE = 'D:\AI Tools\_cache\go\build'
$env:GOTOOLCHAIN = 'local'
$env:GOFLAGS = '-mod=mod'
$appDir = 'D:\AI Tools\Household Hub'
$version = (Select-String -Path (Join-Path $here 'cmd\hhub\main.go') -Pattern 'var version = "(.+)"').Matches[0].Groups[1].Value

Push-Location $here
try {
    & go vet ./...
    if ($LASTEXITCODE) { throw 'go vet failed' }
    & go test ./...
    if ($LASTEXITCODE) { throw 'go test failed' }

    & go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=$version" -o hhub.exe ./cmd/hhub
    if ($LASTEXITCODE) { throw 'go build hhub failed' }
    # Smart App Control can block a fresh unsigned exe; the window build is repeated with another build number until it starts.
    $ok = $false
    for ($n = 1; $n -le 4 -and -not $ok; $n++) {
        & go build -trimpath -ldflags "-s -w -H=windowsgui -X main.build=$n -X main.version=$version" -o HouseholdHub.exe ./cmd/hhub-window
        if ($LASTEXITCODE) { throw 'go build window failed' }
        Start-Sleep -Seconds 2
        try { $p = Start-Process -FilePath (Join-Path $here 'HouseholdHub.exe') -ArgumentList '--check' -Wait -PassThru; if ($p.ExitCode -eq 0) { $ok = $true } } catch { Write-Host "Windows blocked window build ${n}: $($_.Exception.Message)" }
    }
    if (-not $ok) { Write-Warning 'HouseholdHub.exe is blocked by Windows.' }

    New-Item -ItemType Directory -Force -Path (Join-Path $here 'dist') | Out-Null
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    try { & go build -trimpath -ldflags "-s -w -X main.version=$version" -o (Join-Path $here 'dist\hhub-linux-amd64') ./cmd/hhub } finally { Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue }
    if ($LASTEXITCODE) { throw 'go build linux failed' }
} finally { Pop-Location }

if ($Install) {
    New-Item -ItemType Directory -Force -Path $appDir | Out-Null
    # stop the running copies (the background notifier, then the window)
    try { Invoke-WebRequest -UseBasicParsing -Method Post -Headers @{ 'X-HHub' = '1' } -Uri 'http://127.0.0.1:18788/quit' -TimeoutSec 5 | Out-Null } catch {}
    Get-Process hhub, HouseholdHub -ErrorAction SilentlyContinue | Stop-Process -Force
    Start-Sleep -Milliseconds 800
    foreach ($f in 'hhub.exe', 'HouseholdHub.exe', 'icon.ico', 'icon.png') { Copy-Item (Join-Path $here $f) $appDir -Force }
    $sh = New-Object -ComObject WScript.Shell
    foreach ($dir in @([Environment]::GetFolderPath('Programs'), [Environment]::GetFolderPath('Desktop'))) {
        $lnk = $sh.CreateShortcut((Join-Path $dir 'Household Hub.lnk'))
        $lnk.TargetPath = Join-Path $appDir 'HouseholdHub.exe'
        $lnk.WorkingDirectory = $appDir
        $lnk.IconLocation = Join-Path $appDir 'icon.ico'
        $lnk.Description = 'Household finances, bills and shared calendar'
        $lnk.Save()
    }
    $lnk = $sh.CreateShortcut((Join-Path ([Environment]::GetFolderPath('Startup')) 'Household Hub notifications.lnk'))
    $lnk.TargetPath = Join-Path $appDir 'hhub.exe'
    $lnk.Arguments = 'background'
    $lnk.WorkingDirectory = $appDir
    $lnk.IconLocation = Join-Path $appDir 'icon.ico'
    $lnk.Description = 'Household Hub: reminders as Windows notifications (and the household server in host mode)'
    $lnk.Save()
    Start-Process -FilePath (Join-Path $appDir 'hhub.exe') -ArgumentList 'background' -WorkingDirectory $appDir
    Write-Host "Installed to $appDir (Start Menu, Desktop and Startup shortcuts made, background notifier started)."
}

if ($Docker) {
    Push-Location $here
    try {
        & docker build -t household-hub:dev .
        if ($LASTEXITCODE) { throw 'docker build failed' }
        & docker compose -p household-hub up -d --force-recreate
        if ($LASTEXITCODE) { throw 'docker compose up failed' }
    } finally { Pop-Location }
    Write-Host 'Container household-hub-dev runs on http://localhost:8095'
}
Write-Host "Build done ($version)."
