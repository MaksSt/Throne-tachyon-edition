$ErrorActionPreference = 'Stop'
$version = $env:INPUT_VERSION
if ($version -cnotmatch '^\d+\.\d+\.\d+(?:-(?:alpha|beta|rc)\.?\d+)?-tachyon\.\d+$') {
    throw "Недопустимая версия Tachyon Edition: $version"
}
$commit = git rev-parse HEAD
if ($LASTEXITCODE -ne 0 -or $commit -ne $env:GITHUB_SHA) {
    throw 'Коммит сборки отличается от целевого коммита workflow'
}

$work = Join-Path $env:RUNNER_TEMP 'tachyon-package'
$package = New-Item -ItemType Directory (Join-Path $work 'Throne') -Force
Get-ChildItem -LiteralPath $env:DEST -Exclude '*.pdb' | Copy-Item -Destination $package.FullName -Recurse
Copy-Item -LiteralPath LICENSE -Destination $package.FullName
$files = Get-ChildItem -LiteralPath $package.FullName -File -Recurse
if ($files.Name -contains 'tachyon-core.exe') { throw 'Исполняемый файл Tachyon попал в архив' }
@{
    version = $version
    commit = $commit
    platform = 'windows-amd64'
    workflow_run = $env:GITHUB_RUN_ID
} | ConvertTo-Json | Set-Content (Join-Path $package.FullName 'BUILD-INFO.json')
@'
# Throne Tachyon Edition

Полностью распакуйте папку Throne и запустите Throne.exe.
В Preferences > General > Proxy core выберите tachyon-core.
Приложение загрузит ядро из официального репозитория.
Для Windows TUN требуются права администратора.
'@ | Set-Content (Join-Path $package.FullName 'START-HERE.txt')
@{
    live_windows_tun_tested = $false
} | ConvertTo-Json | Set-Content (Join-Path $package.FullName 'TUN-CHECKS.json')
Get-ChildItem -LiteralPath $package.FullName -File -Recurse | Sort-Object FullName | ForEach-Object {
    $relative = [IO.Path]::GetRelativePath($package.FullName, $_.FullName).Replace('\', '/')
    '{0}  {1}' -f (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant(), $relative
} | Set-Content (Join-Path $package.FullName 'SHA256SUMS')

$release = New-Item -ItemType Directory edition-release
$archive = Join-Path $release.FullName "Throne-$version-windows64.zip"
Compress-Archive -LiteralPath $package.FullName -DestinationPath $archive
$unpacked = Join-Path $work 'unpacked'
Expand-Archive -LiteralPath $archive -DestinationPath $unpacked
$install = Join-Path $unpacked 'Throne'
foreach ($name in 'Throne.exe', 'ThroneCore.exe', 'libcronet.dll') {
    if (-not (Test-Path -LiteralPath (Join-Path $install $name) -PathType Leaf)) {
        throw "В архиве отсутствует $name"
    }
}
foreach ($exe in 'Throne.exe', 'ThroneCore.exe') {
    $arguments = if ($exe -eq 'Throne.exe') { @('--headless', '--version') } else { @('--version') }
    $stdout = Join-Path $work "$exe.stdout"
    $stderr = Join-Path $work "$exe.stderr"
    $process = Start-Process -FilePath (Join-Path $install $exe) -ArgumentList $arguments -WorkingDirectory $install `
        -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru
    if (-not $process.WaitForExit(30000)) {
        $process.Kill()
        throw "$exe не завершился после запроса версии"
    }
    $output = (Get-Content -LiteralPath $stdout -Raw).Trim()
    if ($process.ExitCode -ne 0 -or !$output) { throw "$exe не прошёл проверку запуска" }
    if ($exe -eq 'Throne.exe' -and $output -cne "Throne $version") { throw "Неожиданная версия: $output" }
    Write-Output $output
}
'{0}  {1}' -f (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant(), (Split-Path $archive -Leaf) |
    Set-Content (Join-Path $release.FullName 'SHA256SUMS.txt')
