# Releases one version of Household Hub: tests, version bump, commit, tag, push, Docker image, Unraid template.
#   .\release.ps1 -Version 1.3.0 -Notes "short change note"
param([Parameter(Mandatory)][string]$Version, [Parameter(Mandatory)][string]$Notes)
$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $here
$main = Join-Path $here 'cmd\hhub\main.go'
(Get-Content $main -Raw) -replace 'var version = "[^"]+"', "var version = `"$Version`"" | Set-Content $main -Encoding utf8 -NoNewline
$tpl = Join-Path $here 'unraid\household-hub.xml'
$x = Get-Content $tpl -Raw
$x = $x -replace '<Changes>', "<Changes>${Version}: $([Security.SecurityElement]::Escape($Notes)) "
$x = $x -replace '<Date>[^<]*</Date>', "<Date>$(Get-Date -Format yyyy-MM-dd)</Date>"
Set-Content $tpl $x -Encoding utf8 -NoNewline

& (Join-Path $here 'build.ps1') -Install
if ($LASTEXITCODE) { throw 'build failed' }

git add -A
git -c user.name=aon082910 -c user.email=aon082910@users.noreply.github.com commit -q -m "Household Hub ${Version}: $Notes`n`nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git tag "v$Version"
git push -q origin main --tags
docker build --build-arg VERSION=$Version -t "allornothing/household-hub:$Version" -t allornothing/household-hub:latest .
if ($LASTEXITCODE) { throw 'docker build failed' }
docker push "allornothing/household-hub:$Version"
docker push allornothing/household-hub:latest
docker rmi "allornothing/household-hub:$Version" allornothing/household-hub:latest | Out-Null

$apps = 'D:\AI Projects\AoN-Unraid-Apps'
Copy-Item $tpl (Join-Path $apps 'Household-Hub\household-hub.xml') -Force
Push-Location $apps
git add Household-Hub/household-hub.xml
git -c user.name=aon082910 -c user.email=aon082910@users.noreply.github.com commit -q -m "Household-Hub template: $Version`n`nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push -q
Pop-Location
Write-Host "Released $Version"
