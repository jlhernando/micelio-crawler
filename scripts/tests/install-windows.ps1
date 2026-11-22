# Offline tests on Windows. Downloads are mocked in child PowerShell processes.
$ErrorActionPreference = 'Stop'
$root = Join-Path ([IO.Path]::GetTempPath()) ('micelio-install-test-' + [Guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($root) | Out-Null
$installer = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\install.ps1'))
$wrapper = Join-Path $root 'fixture.ps1'
$wrapperSource = @'
param([string]$Installer, [string]$Destination, [string]$Mode)
$ErrorActionPreference = 'Stop'
function curl.exe {
    $arguments = @($args)
    $Uri = $arguments[-1]
    $outputIndex = [Array]::IndexOf($arguments, '--output')
    $OutFile = if ($outputIndex -ge 0) { $arguments[$outputIndex + 1] } else { '' }
    $protocolIndex = [Array]::IndexOf($arguments, '--proto')
    $redirectIndex = [Array]::IndexOf($arguments, '--proto-redir')
    if ($protocolIndex -lt 0 -or $redirectIndex -lt 0 -or $arguments[$protocolIndex + 1] -ne '=https' -or $arguments[$redirectIndex + 1] -ne '=https') { throw 'Missing HTTPS restrictions' }
    Add-Content -LiteralPath ($Destination + '.requests') -Value $Uri
    $global:LASTEXITCODE = 0
    if ($Mode -eq 'http-error' -or $Mode -eq 'downgrade') { $global:LASTEXITCODE = 22; return }
    if ($Uri -eq 'https://api.github.com/repos/jlhernando/micelio-crawler/releases/latest') { Write-Output '{"tag_name":"v1.16.0"}'; return }
    if ($Mode -eq 'collision') { [IO.File]::WriteAllText((Join-Path $Destination 'micelio.exe'), 'keep') }
    if ($Mode -eq 'directory-collision') { [IO.Directory]::CreateDirectory((Join-Path $Destination 'micelio.exe')) | Out-Null }
    $payload = [Text.Encoding]::UTF8.GetBytes("fixture binary, never executed`n")
    $arch = if ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() -eq 'Arm64') { 'arm64' } else { 'amd64' }
    $asset = "micelio-windows-$arch.exe"
    if ($Uri -notlike 'https://github.com/jlhernando/micelio-crawler/releases/download/v1.16.0/*') { throw "Unpinned URL: $Uri" }
    if ($Uri.EndsWith('/checksums.txt')) {
        if ($Mode -eq 'missing-checksums') { throw 'Simulated HTTP 404 for checksums' }
        $hash = ([BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash($payload))).Replace('-', '')
        if ($Mode -eq 'mismatch') { $hash = '0' * 64 }
        if ($Mode -eq 'malformed') { $hash = 'invalid' }
        $line = "$hash  $asset`n"
        if ($Mode -eq 'duplicate') { $line += $line }
        if ($Mode -eq 'no-entry') { $line = "$hash  unrelated-file`n" }
        [IO.File]::WriteAllText($OutFile, $line)
    } else {
        if (!$Uri.EndsWith('/' + $asset)) { throw "Wrong architecture: $Uri" }
        [IO.File]::WriteAllBytes($OutFile, $payload)
    }
}
if ($Mode -eq 'invalid-version') { & $Installer -InstallDir $Destination -Version 'v1.16.0/other' }
elseif ($Mode -eq 'drive-relative') { & $Installer -InstallDir 'C:micelio-test-relative' }
elseif ($Mode -eq 'root-relative') { & $Installer -InstallDir '\micelio-test-relative' }
else { & $Installer -InstallDir $Destination }
'@
[IO.File]::WriteAllText($wrapper, $wrapperSource)
try {
    $engines = @((Join-Path $PSHOME 'pwsh.exe'))
    $legacy = Get-Command powershell.exe -ErrorAction SilentlyContinue
    if ($legacy) { $engines += $legacy.Source }
    $cases = 0
    foreach ($engine in $engines) {
        foreach ($mode in @('success', 'existing', 'mismatch', 'malformed', 'duplicate', 'no-entry', 'missing-checksums', 'http-error', 'downgrade', 'collision', 'directory-collision', 'invalid-version', 'drive-relative', 'root-relative')) {
            $destination = Join-Path $root ([Guid]::NewGuid().ToString('N') + ' path with spaces')
            $target = Join-Path $destination 'micelio.exe'
            if ($mode -eq 'existing') {
                [IO.Directory]::CreateDirectory($destination) | Out-Null
                [IO.File]::WriteAllText($target, 'private build')
            }
            $output = & $engine -NoProfile -ExecutionPolicy Bypass -File $wrapper -Installer $installer -Destination $destination -Mode $mode 2>&1
            $code = $LASTEXITCODE
            if ($mode -eq 'success') {
                if ($code -ne 0 -or [IO.File]::ReadAllText($target) -ne "fixture binary, never executed`n") { throw "Success case failed: $output" }
            } else {
                if ($code -eq 0) { throw "Unsafe success for $mode" }
                if ($mode -eq 'existing') {
                    if ([IO.File]::ReadAllText($target) -ne 'private build' -or (Test-Path -LiteralPath ($destination + '.requests'))) { throw 'Existing build touched' }
                } elseif ($mode -eq 'collision') {
                    if ([IO.File]::ReadAllText($target) -ne 'keep') { throw 'Late collision overwritten' }
                } elseif ($mode -eq 'directory-collision') {
                    if (@(Get-ChildItem -LiteralPath $target -Force).Count -ne 0) { throw 'Wrote inside destination directory' }
                } elseif (Test-Path -LiteralPath $target) { throw "File installed during failure: $mode" }
                if ($mode -in @('drive-relative', 'root-relative') -and (Test-Path -LiteralPath ($destination + '.requests'))) { throw 'Relative path reached network request' }
            }
            if (Test-Path -LiteralPath $destination) {
                if (@(Get-ChildItem -LiteralPath $destination -Filter '.micelio-install-*' -Force).Count -ne 0) { throw "Staging files leaked: $mode" }
            }
            $cases++
        }
    }
    Write-Output "$cases Windows installation cases passed. No network requests or real user files used."
} finally {
    Remove-Item -LiteralPath $root -Recurse -Force
}
