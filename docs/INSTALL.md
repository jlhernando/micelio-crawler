# Install Micelio

Micelio is an MIT-licensed SEO crawler with a web dashboard that runs on your computer. No account or build tools are needed for a release binary. JavaScript rendering needs Chrome or Chromium; ordinary HTTP crawls do not.

## macOS and Linux

Download the installer from this repository, inspect it if you wish, then run it:

```bash
curl --fail --location --proto '=https' --proto-redir '=https' \
  --output install.sh https://raw.githubusercontent.com/jlhernando/micelio-crawler/main/scripts/install.sh
sh install.sh
```

It detects Intel/x64 or ARM64, resolves the latest public release once, and downloads the binary and checksum file from that exact version. Installation stops if the checksum is missing, ambiguous or incorrect. You need `curl` and either `sha256sum` or `shasum`, normally available on macOS and common Linux distributions.

The executable goes in your user account, without `sudo`. Start it by its full path:

```bash
"$HOME/.local/bin/micelio" ui
```

Open http://localhost:3100. For the shorter `micelio` command in the current terminal, add its directory to the current session:

```bash
export PATH="$HOME/.local/bin:$PATH"
micelio --version
```

The installer does not edit your shell profile. You can add that PATH setting to your profile yourself if you want it to persist.

macOS builds are unsigned and not notarized. If macOS blocks launch, follow **System Settings > Privacy & Security > Open Anyway** for the binary you downloaded and verified. The installer does not remove quarantine or change Gatekeeper settings.

## Windows

Open PowerShell on Windows 10 or later. Download the script and review it before running it:

```powershell
Invoke-WebRequest -Uri https://raw.githubusercontent.com/jlhernando/micelio-crawler/main/scripts/install.ps1 -OutFile install.ps1
Get-Content .\install.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
```

The execution-policy option applies only to that PowerShell process. It lets the downloaded unsigned script run; it does not change your saved policy. The script detects x64 or ARM64 and requires a matching SHA-256 checksum from the same release as the binary. It uses `curl.exe`, included in current Windows 10 and Windows 11, and refuses redirects to HTTP.

The executable goes in your user account. No administrator access is needed:

```powershell
& "$env:LOCALAPPDATA\Micelio\bin\micelio.exe" ui
```

Open http://localhost:3100. If Windows warns about the unrecognized application, confirm the repository and verify your download before deciding whether to allow it. The script does not disable Windows protections.

For the shorter command in the current PowerShell session:

```powershell
$env:Path = "$env:LOCALAPPDATA\Micelio\bin;$env:Path"
micelio --version
```

## Existing installations and custom builds

These are **fresh-install scripts**, not updaters. They refuse to replace an existing file or symlink. If another `micelio` is already on PATH, the installer asks for an explicit separate destination. That avoids silently replacing or shadowing another build.

```bash
sh install.sh --install-dir "$HOME/micelio-public/bin" --version v1.16.0
```

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -InstallDir "$env:LOCALAPPDATA\MicelioPublic\bin" -Version v1.16.0
```

The scripts leave application bundles, Go installations, existing processes and PATH unchanged. They do not start the dashboard or send telemetry. Public and custom builds can share the default `~/.micelio` data directory; use only the intended build for your existing data, and do not start a second dashboard on the same port. A private fork may include features absent from the public binary.

## Manual downloads

If you prefer not to run an installer script, use the [six direct download links](../README.md#download). Choose the version on the [releases page](https://github.com/jlhernando/micelio-crawler/releases), then download **both** your binary and `checksums.txt` from that same version. Do not mix a pinned binary with a checksum file from a changing `latest` URL.

On macOS or Linux, hash just your downloaded file and compare the result with its exact filename in `checksums.txt`:

```bash
# Example for macOS Apple Silicon
shasum -a 256 micelio-darwin-arm64
```

On Windows:

```powershell
Get-FileHash .\micelio-windows-amd64.exe -Algorithm SHA256
```

Do not run the binary if there is no matching entry or the digest differs. On Unix, make a verified binary executable with `chmod +x` and run it by its full path. On Windows, use `&` followed by the executable's path. Downloads are executables that launch the web dashboard, not graphical setup packages.

## Try a first audit

In **New Crawl**, enter your own homepage, set a limit of 100 pages and start with JavaScript rendering off. In the finished results, open **Issues** and inspect the affected URLs. See the [sample walkthrough](DEMO.md) and [user guide](USER-GUIDE.md) for the next steps.
