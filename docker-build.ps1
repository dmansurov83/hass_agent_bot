#!/usr/bin/env pwsh
<#
.SYNOPSIS
    Сборка Docker-образа и публикация в Docker Hub.
    Версия, имя образа и платформы берутся из build.yaml.
#>

$ErrorActionPreference = "Stop"

$buildConfig = Join-Path $PSScriptRoot "build.yaml"
if (-not (Test-Path $buildConfig)) {
    Write-Error "build.yaml not found at $buildConfig"
    exit 1
}

$yaml = Get-Content $buildConfig -Raw

$versionMatch = [regex]::Match($yaml, 'version:\s*"([^"]+)"')
if (-not $versionMatch.Success) {
    Write-Error "version not found in build.yaml (add version: ""X.Y.Z"")"
    exit 1
}
$version = $versionMatch.Groups[1].Value

$imageMatch = [regex]::Match($yaml, 'image:\s*([^\s]+)')
if (-not $imageMatch.Success) {
    Write-Error "image not found in build.yaml (add image: user/name)"
    exit 1
}
$imageName = $imageMatch.Groups[1].Value

$platformMatch = [regex]::Match($yaml, 'platforms:\s*([^\s]+)')
$platform = if ($platformMatch.Success) { $platformMatch.Groups[1].Value } else { "linux/amd64" }

Write-Host "==> Build $imageName : $version (platform: $platform)" -ForegroundColor Cyan

docker buildx build --platform $platform -t "$imageName`:latest" -t "$imageName`:$version" .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "==> Push $imageName : $version" -ForegroundColor Cyan

docker push "$imageName`:latest"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
docker push "$imageName`:$version"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host ""
Write-Host "Done!" -ForegroundColor Green
Write-Host "  $imageName`:latest"
Write-Host "  $imageName`:$version"