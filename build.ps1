param(
    [string]$Go = '',
    [string]$ScpslManaged = 'C:\Program Files (x86)\Steam\steamapps\common\SCP Secret Laboratory Dedicated Server\SCPSL_Data\Managed',
    [string]$ExiledRefs = '',
    [switch]$SkipTests
)
$ErrorActionPreference = 'Stop'
$projectRoot = $PSScriptRoot
if (!$Go) {
    $portableGo = Join-Path $projectRoot '.tools\go\bin\go.exe'
    if (Test-Path -LiteralPath $portableGo) { $Go = $portableGo }
    else { $Go = (Get-Command go -ErrorAction Stop).Source }
}
if (!$ExiledRefs) { $ExiledRefs = Join-Path $projectRoot '.tools\exiled\SCP Secret Laboratory\LabAPI\dependencies\global' }
foreach ($path in @((Join-Path $ScpslManaged 'LabApi.dll'), (Join-Path $ExiledRefs 'Exiled.API.dll'))) {
    if (!(Test-Path -LiteralPath $path)) { throw "Missing reference: $path (see docs/development.md)" }
}
$runtimeRoot = Join-Path $projectRoot 'dist\QQBotLite-win-x64'
foreach ($path in @($runtimeRoot, (Join-Path $runtimeRoot 'plugins\LabAPI'), (Join-Path $runtimeRoot 'plugins\EXILED'), (Join-Path $runtimeRoot 'docs'))) {
    New-Item -ItemType Directory -Path $path -Force | Out-Null
}
$savedEnvironment = @{}
$environmentNames = @('GOPATH','GOCACHE','GOENV','GOOS','GOARCH','CGO_ENABLED','TMP','TEMP','GOTMPDIR','QQBOTLITE_PLUGIN_HARNESS','QQBOTLITE_BINARY')
foreach ($name in $environmentNames) { $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name,'Process') }
try {
    $tempPath = Join-Path $projectRoot '.tools\tmp'
    New-Item -ItemType Directory -Path $tempPath -Force | Out-Null
    $env:GOPATH = Join-Path $projectRoot '.tools\gopath'
    $env:GOCACHE = Join-Path $projectRoot '.tools\gocache'
    $env:GOENV = 'off'
    $env:TMP = $tempPath; $env:TEMP = $tempPath; $env:GOTMPDIR = $tempPath
    & dotnet build (Join-Path $projectRoot 'plugins\LabApi\QQBotLite.LabApi.csproj') -c Release --nologo "-p:ScpslManaged=$ScpslManaged"
    if ($LASTEXITCODE -ne 0) { throw 'LabAPI build failed' }
    & dotnet build (Join-Path $projectRoot 'plugins\Exiled\QQBotLite.Exiled.csproj') -c Release --nologo "-p:ScpslManaged=$ScpslManaged" "-p:ExiledRefs=$ExiledRefs"
    if ($LASTEXITCODE -ne 0) { throw 'EXILED build failed' }
    $env:GOOS='windows'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
    $env:QQBOTLITE_BINARY = Join-Path $runtimeRoot 'QQBotLite.exe'
    & $Go -C (Join-Path $projectRoot 'bot') build -trimpath '-ldflags=-s -w' -o $env:QQBOTLITE_BINARY .
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
    if (!$SkipTests) {
        & dotnet build (Join-Path $projectRoot 'tests\PluginHarness\PluginHarness.csproj') -c Release --nologo
        if ($LASTEXITCODE -ne 0) { throw 'Plugin harness build failed' }
        $env:QQBOTLITE_PLUGIN_HARNESS = Join-Path $projectRoot 'tests\PluginHarness\bin\Release\net10.0\PluginHarness.dll'
        & $Go -C (Join-Path $projectRoot 'bot') vet ./...
        if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
        & $Go -C (Join-Path $projectRoot 'bot') test ./... -count=1
        if ($LASTEXITCODE -ne 0) { throw 'Tests failed' }
    }
    Copy-Item -LiteralPath (Join-Path $projectRoot 'plugins\LabApi\bin\Release\net48\QQBotLite.LabApi.dll') -Destination (Join-Path $runtimeRoot 'plugins\LabAPI') -Force
    Copy-Item -LiteralPath (Join-Path $projectRoot 'plugins\Exiled\bin\Release\net48\QQBotLite.Exiled.dll') -Destination (Join-Path $runtimeRoot 'plugins\EXILED') -Force
    foreach ($name in @('README.md','LICENSE','config.example.json','THIRD_PARTY_NOTICES.txt')) {
        Copy-Item -LiteralPath (Join-Path $projectRoot $name) -Destination $runtimeRoot -Force
    }
    Copy-Item -Path (Join-Path $projectRoot 'scripts\*.cmd') -Destination $runtimeRoot -Force
    Copy-Item -Path (Join-Path $projectRoot 'docs\*.md') -Destination (Join-Path $runtimeRoot 'docs') -Force
    & (Join-Path $projectRoot 'package.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'Packaging failed' }
    Write-Host "Built: $runtimeRoot"
} finally {
    foreach ($name in $environmentNames) { [Environment]::SetEnvironmentVariable($name,$savedEnvironment[$name],'Process') }
}
