$ErrorActionPreference='Stop'
$projectRoot=$PSScriptRoot
$distRoot=Join-Path $projectRoot 'dist'
$runtimeRoot=Join-Path $distRoot 'QQBotLite-win-x64'
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
function Write-Zip([string]$Destination,[string]$Root,[string[]]$Files,[string]$Prefix) {
    $stream=[IO.File]::Open($Destination,[IO.FileMode]::Create)
    try {
        $archive=[IO.Compression.ZipArchive]::new($stream,[IO.Compression.ZipArchiveMode]::Create)
        try {
            foreach($file in $Files) {
                $relative=$file.Substring($Root.Length).TrimStart('\','/').Replace('\','/')
                [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($archive,$file,"$Prefix/$relative",[IO.Compression.CompressionLevel]::Optimal) | Out-Null
            }
        } finally { $archive.Dispose() }
    } finally { $stream.Dispose() }
}
# Select distribution files explicitly: never include real configs, generated tokens, logs or tools.
$runtimeFiles=@()
foreach($name in @('QQBotLite.exe','README.md','LICENSE','VERSION','CHANGELOG.zh.md','config.example.json','THIRD_PARTY_NOTICES.txt','start.cmd','configure.cmd','check-config.cmd')) {
    $runtimeFiles+=Join-Path $runtimeRoot $name
}
$runtimeFiles+=@(Get-ChildItem -LiteralPath (Join-Path $runtimeRoot 'plugins') -File -Recurse -Filter '*.dll' | ForEach-Object FullName)
$runtimeFiles+=@(Get-ChildItem -LiteralPath (Join-Path $runtimeRoot 'docs') -File -Filter '*.md' | ForEach-Object FullName)
$runtimeFiles+=@(Get-ChildItem -LiteralPath (Join-Path $runtimeRoot 'assets\readme') -File -Filter '*.svg' | ForEach-Object FullName)
Write-Zip (Join-Path $distRoot 'QQBotLite-win-x64.zip') $runtimeRoot $runtimeFiles 'QQBotLite'
$sourceFiles=@()
foreach($name in @('.gitignore','README.md','LICENSE','VERSION','CHANGELOG.zh.md','config.example.json','THIRD_PARTY_NOTICES.txt','build.ps1','package.ps1')) { $sourceFiles+=Join-Path $projectRoot $name }
$sourceFiles+=@(Get-ChildItem -LiteralPath (Join-Path $projectRoot 'bot') -File | Where-Object { $_.Extension -eq '.go' -or $_.Name -in @('go.mod','go.sum') } | ForEach-Object FullName)
foreach($dir in @('plugins','tests','docs','scripts')) {
    $sourceFiles+=@(Get-ChildItem -LiteralPath (Join-Path $projectRoot $dir) -File -Recurse | Where-Object { $_.FullName -notmatch '[\\/](bin|obj)[\\/]' -and $_.Extension -in @('.cs','.csproj','.props','.md','.cmd') } | ForEach-Object FullName)
}
$sourceFiles+=@(Get-ChildItem -LiteralPath (Join-Path $projectRoot 'assets\readme') -File -Filter '*.svg' | ForEach-Object FullName)
Write-Zip (Join-Path $distRoot 'QQBotLite-source.zip') $projectRoot $sourceFiles 'QQBotLite'
$manifest=@()
foreach($path in @((Join-Path $distRoot 'QQBotLite-win-x64.zip'),(Join-Path $distRoot 'QQBotLite-source.zip'))+$runtimeFiles) {
    $hash=Get-FileHash -LiteralPath $path -Algorithm SHA256
    $relative=$path.Substring($distRoot.Length).TrimStart('\','/').Replace('\','/')
    $relative=$relative.Replace('QQBotLite-win-x64/','QQBotLite/')
    $manifest+="$($hash.Hash.ToLower())  $relative"
}
[IO.File]::WriteAllLines((Join-Path $distRoot 'SHA256SUMS.txt'),$manifest,[Text.UTF8Encoding]::new($false))
Write-Host 'Packaged runtime, source and SHA256SUMS.txt'
