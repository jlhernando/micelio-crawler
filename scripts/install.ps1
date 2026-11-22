# Fresh public installations only. Does not require administrator access.
[CmdletBinding()]
param(
    [string]$Version = '',
    [string]$InstallDir = ''
)
$ErrorActionPreference = 'Stop'
$stage = $null
function Invoke-MicelioDownload {
    param([string]$Uri, [string]$OutFile = '')
    $curlArgs = @('--fail', '--silent', '--show-error', '--location', '--proto', '=https', '--proto-redir', '=https', '--connect-timeout', '15', '--max-time', '300')
    if ($OutFile) { $curlArgs += @('--output', $OutFile) }
    & curl.exe @curlArgs $Uri
    if ($LASTEXITCODE -ne 0) { throw "Download failed: $Uri" }
}
try {
    if ($env:OS -ne 'Windows_NT') { throw 'This installer requires Windows.' }
    $explicitDir = $PSBoundParameters.ContainsKey('InstallDir')
    if (!$InstallDir) {
        if (!$env:LOCALAPPDATA) { throw 'LOCALAPPDATA is required.' }
        $InstallDir = Join-Path $env:LOCALAPPDATA 'Micelio\bin'
    }
    if ($InstallDir -notmatch '^(?:[a-zA-Z]:[\\/]|\\\\[^\\/?]+\\[^\\/?]+(?:\\|$))') {
        throw 'Use a fully qualified installation directory, such as C:\Users\you\Micelio.'
    }
    $InstallDir = [IO.Path]::GetFullPath($InstallDir)
    $target = Join-Path $InstallDir 'micelio.exe'
    if (Get-Item -LiteralPath $target -Force -ErrorAction SilentlyContinue) {
        throw "$target already exists; nothing changed."
    }
    $existing = Get-Command micelio -ErrorAction SilentlyContinue
    if ($existing) {
        Write-Warning "Another Micelio exists at $($existing.Source); it will remain untouched."
        if (!$explicitDir) { throw 'Choose a separate directory with -InstallDir to keep both builds.' }
    }
    $nativeArch = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    switch ($nativeArch) {
        'X64' { $arch = 'amd64' }
        'Arm64' { $arch = 'arm64' }
        default { throw "Unsupported processor: $nativeArch" }
    }
    if (!(Get-Command curl.exe -ErrorAction SilentlyContinue)) { throw 'curl.exe is required, as included with current Windows 10 and Windows 11.' }
    $repo = 'https://github.com/jlhernando/micelio-crawler'
    if (!$Version) {
        $release = Invoke-MicelioDownload 'https://api.github.com/repos/jlhernando/micelio-crawler/releases/latest' | ConvertFrom-Json
        $Version = $release.tag_name
    }
    if ($Version -cnotmatch '^v[0-9][a-zA-Z0-9.+-]*$') { throw 'Invalid release version.' }
    $asset = "micelio-windows-$arch.exe"
    [IO.Directory]::CreateDirectory($InstallDir) | Out-Null
    $stage = Join-Path $InstallDir ('.micelio-install-' + [Guid]::NewGuid().ToString('N'))
    [IO.Directory]::CreateDirectory($stage) | Out-Null
    $binary = Join-Path $stage $asset
    $checksums = Join-Path $stage 'checksums.txt'
    Invoke-MicelioDownload "$repo/releases/download/$Version/$asset" $binary
    Invoke-MicelioDownload "$repo/releases/download/$Version/checksums.txt" $checksums
    $entries = @(Get-Content -LiteralPath $checksums | Where-Object {
        $_ -match ('\s+\*?' + [regex]::Escape($asset) + '\s*$')
    })
    if ($entries.Count -ne 1 -or $entries[0] -notmatch ('^([0-9a-fA-F]{64})\s+\*?' + [regex]::Escape($asset) + '\s*$')) {
        throw 'Missing, duplicate or invalid checksum entry.'
    }
    $expected = $Matches[1]
    $actual = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash
    if ($actual -ine $expected) { throw 'SHA-256 mismatch; nothing installed.' }
    # The two-argument Move overload refuses an existing destination.
    [IO.File]::Move($binary, $target)
    Write-Output "Installed public Micelio $Version at $target"
    Write-Output 'Start the dashboard with the full path, or add this directory to PATH yourself:'
    Write-Output "& '$($target.Replace("'", "''"))' ui"
} catch {
    Write-Error "Micelio install: $($_.Exception.Message)"
    exit 1
} finally {
    if ($stage -and (Test-Path -LiteralPath $stage)) { Remove-Item -LiteralPath $stage -Recurse -Force }
}
