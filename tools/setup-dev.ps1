<#
.SYNOPSIS
  Set up a Windows machine to build, test, lint and debug pawnstar-go: the toolchain, the tools and the editor.

.DESCRIPTION
  Each component is checked first and installed only when it is missing or not the version the repo wants, so the
  script is safe to run again at any time. Everything goes into the user's own profile and needs no administrator.
  tools/setup-dev.sh is the same for Linux and macOS. VS Code's "setup environment" and "check environment" tasks run
  the one for the platform.

  What it sets up:
    - Git for Windows, whose bash runs tools/version.sh and the Makefile's recipes
    - Go, the newest 1.26.x (go.mod's line) in %LOCALAPPDATA%\Programs\go: never 1.27 or later, whose simd/archsimd
      API the amd64 NNUE kernels do not compile against
    - golangci-lint, the newest release of the line CI pins (.github/workflows/ci.yml), and gopls and dlv for VS Code's
      Go extension, in %USERPROFILE%\go\bin
    - MSYS2 in C:\msys64 (or $env:MSYS2_ROOT) with its CLANG64 clang, the C compiler cgo needs for the race detector
      (`make race`), and mingw32-make, a native make that runs the Makefile from Git Bash
    - cgo pointed at that clang (go env -w CC=... CGO_ENABLED=1)
    - the repo's git hooks (core.hooksPath .githooks) and the VS Code extensions .vscode/extensions.json recommends

.PARAMETER Check
  Report what is present and what is missing, install nothing, and exit 1 if anything is missing.

.PARAMETER NoPersist
  Change nothing for good - neither the user's PATH nor Go's own settings (go env -w), only this process's
  environment: for a test run in a scratch profile.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File tools\setup-dev.ps1           # set up
  powershell -ExecutionPolicy Bypass -File tools\setup-dev.ps1 -Check    # report only
#>
param([switch]$Check, [switch]$NoPersist)
# Continue, not Stop: Windows PowerShell 5.1 makes a native command's stderr a terminating error under Stop, even
# redirected (go and pacman print their progress there). The cmdlets that must stop say so; native commands' exit codes
# are checked.
$ErrorActionPreference = 'Continue'
$ProgressPreference = 'SilentlyContinue'
Set-Location (Split-Path -Parent $PSScriptRoot)
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

# ---------------------------------------------------------------------------------------------------------------------
# Helpers (the same in every reckless-technology repo's setup script).
$script:missing = 0
function Write-Ok([string]$name, [string]$detail) { Write-Host ("  ok       {0,-24} {1}" -f $name, $detail) -ForegroundColor Green }
function Write-Missing([string]$name, [string]$detail) {
  $script:missing++
  Write-Host ("  missing  {0,-24} {1}" -f $name, $detail) -ForegroundColor Yellow
}
function Write-Installing([string]$name, [string]$detail) { Write-Host ("  install  {0,-24} {1}" -f $name, $detail) -ForegroundColor Cyan }

# Put a directory on this process's PATH (first, or last with -Append) and, unless -NoPersist, on the user's PATH for
# every new shell; under -Check, report it missing from the user's PATH instead.
function Add-ToPath([string]$dir, [switch]$Append) {
  if (($env:PATH -split ';') -notcontains $dir) {
    if ($Append) { $env:PATH = "$env:PATH;$dir" } else { $env:PATH = "$dir;$env:PATH" }
  }
  if ($NoPersist) { return }
  $user = @(([Environment]::GetEnvironmentVariable('Path', 'User')) -split ';' | Where-Object { $_ })
  $machine = @(([Environment]::GetEnvironmentVariable('Path', 'Machine')) -split ';' | Where-Object { $_ })
  if ($user -contains $dir -or $machine -contains $dir) { return }
  if ($Check) { Write-Missing 'PATH' "$dir is not on the user's PATH"; return }
  if ($Append) { $user = $user + $dir } else { $user = @($dir) + $user }
  [Environment]::SetEnvironmentVariable('Path', $user -join ';', 'User')
  Write-Installing 'PATH' $dir
}

# Download a file and refuse it unless its SHA-256 is the one given.
function Get-VerifiedFile([string]$url, [string]$sha256, [string]$out) {
  Invoke-WebRequest -Uri $url -OutFile $out -UseBasicParsing -ErrorAction Stop
  $got = (Get-FileHash $out -Algorithm SHA256).Hash
  if ($got -ne $sha256.ToUpper()) { Remove-Item $out; throw "SHA-256 mismatch for $url (got $got, want $sha256)" }
}

# A downloaded text file's text, whatever type Invoke-WebRequest hands back.
function Get-Text([string]$url) {
  $content = (Invoke-WebRequest -Uri $url -UseBasicParsing -ErrorAction Stop).Content
  if ($content -is [byte[]]) { return [Text.Encoding]::ASCII.GetString($content) }
  return $content
}

function New-ScratchDir {
  $dir = Join-Path ([IO.Path]::GetTempPath()) ("setup-dev-" + [Guid]::NewGuid().ToString('N').Substring(0, 8))
  New-Item -ItemType Directory -Path $dir -ErrorAction Stop | Out-Null
  return $dir
}

# Unpack a zip with Windows' own tar (bsdtar), many times faster than Expand-Archive on Go's 15000 files.
function Expand-Zip([string]$zip, [string]$into) {
  New-Item -ItemType Directory -Force -Path $into -ErrorAction Stop | Out-Null
  $tar = Join-Path $env:SystemRoot 'System32\tar.exe'
  if (Test-Path $tar) {
    & $tar -xf $zip -C $into
    if ($LASTEXITCODE -ne 0) { throw "tar could not unpack $zip" }
  }
  else { Expand-Archive -Path $zip -DestinationPath $into -Force -ErrorAction Stop }
}

function Set-GitHooks {
  if (-not (Test-Path '.githooks')) { return }
  $current = git config core.hooksPath
  if ($current -eq '.githooks') { Write-Ok 'git hooks' 'core.hooksPath .githooks'; return }
  if ($Check) { Write-Missing 'git hooks' 'core.hooksPath is not .githooks'; return }
  git config core.hooksPath .githooks
  Write-Installing 'git hooks' 'core.hooksPath .githooks'
}

# The extensions .vscode/extensions.json recommends, when VS Code's command line is on the PATH.
function Install-VSCodeExtensions {
  $code = Get-Command code -ErrorAction SilentlyContinue
  if (-not $code -or -not (Test-Path '.vscode/extensions.json')) { return }
  $json = (Get-Content '.vscode/extensions.json' -Raw) -replace '(?m)^\s*//.*$', '' | ConvertFrom-Json
  $installed = & $code.Source --list-extensions 2>$null
  foreach ($ext in $json.recommendations) {
    if ($installed -contains $ext) { Write-Ok 'VS Code extension' $ext; continue }
    if ($Check) { Write-Missing 'VS Code extension' $ext; continue }
    Write-Installing 'VS Code extension' $ext
    & $code.Source --install-extension $ext --force | Out-Null
  }
}

# ---------------------------------------------------------------------------------------------------------------------
# Go-specific helpers.

# A Go binary's own version ("1.26.9"), never a toolchain it would switch to: GOTOOLCHAIN=local for the question.
function Get-GoVersion([string]$exe) {
  $saved = $env:GOTOOLCHAIN
  $env:GOTOOLCHAIN = 'local'
  $out = & $exe version 2>$null
  $env:GOTOOLCHAIN = $saved
  if ("$out" -match 'go version go(\d+\.\d+(\.\d+)?)') { return $Matches[1] }
  return $null
}
function Test-Line([string]$version, [string]$line) { return $version -and ($version -eq $line -or $version.StartsWith("$line.")) }

# Run a command in MSYS2's own bash (a login shell, so its /usr/bin leads the PATH).
function Invoke-Msys([string]$command, [switch]$MayFail) {
  & (Join-Path $msys 'usr\bin\bash.exe') -lc $command
  if ($LASTEXITCODE -ne 0 -and -not $MayFail) { throw "MSYS2's bash exited with $LASTEXITCODE running: $command" }
}

# ---------------------------------------------------------------------------------------------------------------------
Write-Host "pawnstar-go: $(if ($Check) { 'checking' } else { 'setting up' }) the Windows development environment"

# The versions the repo pins: go.mod's line (1.26) and the golangci-lint line CI runs (v2.12).
$goLine = (Select-String -Path go.mod -Pattern '^go (\d+\.\d+)' -ErrorAction Stop | Select-Object -First 1).Matches[0].Groups[1].Value
$lintLine = [regex]::Match((Get-Content .github/workflows/ci.yml -Raw), 'golangci-lint-action@[\s\S]*?version:\s*v(\d+\.\d+)').Groups[1].Value
if (-not $goLine -or -not $lintLine) { throw 'could not read the Go line from go.mod or the golangci-lint line from ci.yml' }
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }

# Git for Windows: git itself, and the bash tools/version.sh and the Makefile need - never WSL's System32\bash.exe.
$gitBash = $null
$git = Get-Command git -ErrorAction SilentlyContinue
if ($git) {
  $root = (Split-Path (Split-Path (Split-Path (git --exec-path))))
  foreach ($candidate in "$root\bin\bash.exe", "$env:ProgramFiles\Git\bin\bash.exe") {
    if (Test-Path $candidate) { $gitBash = $candidate; break }
  }
}
if ($gitBash) { Write-Ok 'Git for Windows' "$(git --version) ($gitBash)" }
elseif ($Check) { Write-Missing 'Git for Windows' 'git and its bash' }
else {
  Write-Installing 'Git for Windows' 'through winget'
  winget install --id Git.Git -e --accept-package-agreements --accept-source-agreements
  Write-Host '  Git for Windows is installed: open a new terminal and run this script again.'
  exit 0
}

# Go: whatever go is on the PATH if it is go.mod's line, else the newest release of that line, in the user's profile.
$goRoot = Join-Path $env:LOCALAPPDATA 'Programs\go'
$goRootBin = Join-Path $goRoot 'bin'
if (Test-Path (Join-Path $goRootBin 'go.exe')) { Add-ToPath $goRootBin }
$goCmd = Get-Command go -ErrorAction SilentlyContinue
$goVersion = if ($goCmd) { Get-GoVersion $goCmd.Source }
if (Test-Line $goVersion $goLine) { Write-Ok 'Go' "go$goVersion ($($goCmd.Source))" }
elseif ($Check) { Write-Missing 'Go' "go$goLine.x$(if ($goVersion) { " (found go$goVersion, $($goCmd.Source))" })" }
else {
  $index = Get-Text 'https://go.dev/dl/?mode=json&include=all'
  $pattern = '"filename":\s*"(go' + [regex]::Escape($goLine) + '\.(\d+)\.windows-' + $arch + '\.zip)"[^}]*?"sha256":\s*"([0-9a-f]{64})"'
  $best = [regex]::Matches($index, $pattern) | Sort-Object { [int]$_.Groups[2].Value } | Select-Object -Last 1
  if (-not $best) { throw "go.dev lists no go$goLine.x for windows-$arch" }
  $file = $best.Groups[1].Value
  Write-Installing 'Go' "$file from go.dev into $goRoot"
  $scratch = New-ScratchDir
  Get-VerifiedFile "https://go.dev/dl/$file" $best.Groups[3].Value (Join-Path $scratch $file)
  if (Test-Path $goRoot) { Remove-Item -Recurse -Force $goRoot -ErrorAction Stop }
  Expand-Zip (Join-Path $scratch $file) (Split-Path $goRoot)
  Remove-Item -Recurse -Force $scratch
  Add-ToPath $goRootBin
  $goCmd = Get-Command go -ErrorAction SilentlyContinue
  $goVersion = if ($goCmd) { Get-GoVersion $goCmd.Source }
  if (-not (Test-Line $goVersion $goLine)) { Write-Missing 'Go' "installed, but the go on the PATH is go$goVersion ($($goCmd.Source)): put $goRootBin ahead of it" }
}

$haveGo = Test-Line $goVersion $goLine
if ($haveGo) {
  # Where go install puts the tools: GOBIN, else the first GOPATH's bin (%USERPROFILE%\go\bin by default).
  $toolBin = (& go env GOBIN)
  if (-not $toolBin) { $toolBin = Join-Path (((& go env GOPATH) -split ';')[0]) 'bin' }
  if (Test-Path $toolBin) { Add-ToPath $toolBin }

  # gopls and dlv, which VS Code's Go extension runs for the code model and the debugger. A release that needs a newer
  # Go to build (gopls 0.24 needs 1.27) has go install fetch that toolchain into the module cache for its own build
  # alone; the repo's go stays 1.26.
  foreach ($tool in @(@{ Name = 'gopls'; Package = 'golang.org/x/tools/gopls@latest' },
      @{ Name = 'dlv'; Package = 'github.com/go-delve/delve/cmd/dlv@latest' })) {
    $cmd = Get-Command $tool.Name -ErrorAction SilentlyContinue
    if ($cmd) { Write-Ok $tool.Name $cmd.Source }
    elseif ($Check) { Write-Missing $tool.Name "go install $($tool.Package)" }
    else {
      Write-Installing $tool.Name "go install $($tool.Package)"
      go install $tool.Package
      if ($LASTEXITCODE -ne 0) { throw "go install $($tool.Package) exited with $LASTEXITCODE" }
      Add-ToPath $toolBin
    }
  }

  # golangci-lint: the newest release of CI's line, from its GitHub release, checked against its checksums.txt.
  $lint = Get-Command golangci-lint -ErrorAction SilentlyContinue
  $lintVersion = if ($lint -and ("$(& $lint.Source version 2>$null)" -match 'version v?(\d+\.\d+\.\d+)')) { $Matches[1] }
  if (Test-Line $lintVersion $lintLine) { Write-Ok 'golangci-lint' "$lintVersion ($($lint.Source))" }
  elseif ($Check) { Write-Missing 'golangci-lint' "v$lintLine.x$(if ($lintVersion) { " (found $lintVersion)" })" }
  else {
    $major = [int]$lintLine.Split('.')[0]
    $module = 'github.com/golangci/golangci-lint' + $(if ($major -ge 2) { "/v$major" })
    $latest = (Get-Text "https://proxy.golang.org/$module/@v/list") -split '\s+' |
      Where-Object { $_ -match ('^v' + [regex]::Escape($lintLine) + '\.(\d+)$') } |
      Sort-Object { [int]($_ -replace '^.*\.', '') } | Select-Object -Last 1
    if (-not $latest) { throw "no golangci-lint v$lintLine.x is published" }
    $version = $latest.TrimStart('v')
    $file = "golangci-lint-$version-windows-$arch.zip"
    $base = "https://github.com/golangci/golangci-lint/releases/download/v$version"
    $sums = Get-Text "$base/golangci-lint-$version-checksums.txt"
    $sum = [regex]::Match($sums, '(?m)^([0-9a-f]{64})\s+' + [regex]::Escape($file) + '\s*$').Groups[1].Value
    if (-not $sum) { throw "$file is not in the release's checksums.txt" }
    Write-Installing 'golangci-lint' "$version from its GitHub release into $toolBin"
    $scratch = New-ScratchDir
    Get-VerifiedFile "$base/$file" $sum (Join-Path $scratch $file)
    Expand-Zip (Join-Path $scratch $file) $scratch
    New-Item -ItemType Directory -Force -Path $toolBin -ErrorAction Stop | Out-Null
    Copy-Item (Join-Path $scratch "golangci-lint-$version-windows-$arch\golangci-lint.exe") $toolBin -Force -ErrorAction Stop
    Remove-Item -Recurse -Force $scratch
    Add-ToPath $toolBin
  }
}
elseif ($Check) { Write-Missing 'gopls, dlv, golangci-lint' 'after Go' }

# MSYS2 with its CLANG64 clang (cgo's C compiler, which the race detector needs) and mingw32-make (a native GNU make;
# MSYS2's own make, an MSYS program, is handed only the PATH when Git Bash starts it, so Go finds no temporary folder).
$msys = if ($env:MSYS2_ROOT) { $env:MSYS2_ROOT } else { 'C:\msys64' }
$clang64 = Join-Path $msys 'clang64\bin'
$packages = 'mingw-w64-clang-x86_64-clang', 'mingw-w64-clang-x86_64-make'
$ranPacman = $false
if (Test-Path (Join-Path $msys 'usr\bin\pacman.exe')) { Write-Ok 'MSYS2' $msys }
elseif ($Check) { Write-Missing 'MSYS2' "$msys (clang for the race detector, and make)" }
else {
  # The newest dated release (not the nightly), checked against the SHA-256 digest GitHub records for the asset.
  $releases = Invoke-RestMethod -Uri 'https://api.github.com/repos/msys2/msys2-installer/releases?per_page=20' -UseBasicParsing -ErrorAction Stop
  $release = $releases | Where-Object { $_.tag_name -match '^\d{4}-\d{2}-\d{2}$' } | Select-Object -First 1
  $asset = $release.assets | Where-Object { $_.name -match '^msys2-base-x86_64-\d{8}\.sfx\.exe$' } | Select-Object -First 1
  $digest = if ($asset) { [regex]::Match("$($asset.digest)", '^sha256:([0-9a-f]{64})$').Groups[1].Value }
  if (-not $digest) { throw 'no msys2-base sfx with a SHA-256 digest in the latest MSYS2 release' }
  Write-Installing 'MSYS2' "$($asset.name) into $msys"
  $scratch = New-ScratchDir
  $sfx = Join-Path $scratch $asset.name
  Get-VerifiedFile $asset.browser_download_url $digest $sfx
  $parent = Split-Path $msys
  New-Item -ItemType Directory -Force -Path $parent -ErrorAction Stop | Out-Null
  $p = Start-Process -FilePath $sfx -ArgumentList @('-y', "-o$parent") -Wait -PassThru -NoNewWindow
  if ($p.ExitCode -ne 0) { throw "the MSYS2 archive exited with $($p.ExitCode)" }
  if ((Split-Path $msys -Leaf) -ne 'msys64') { Rename-Item (Join-Path $parent 'msys64') (Split-Path $msys -Leaf) -ErrorAction Stop }
  Remove-Item -Recurse -Force $scratch
  # The first login initialises the keyring; then the core update, which may end its own shell, and the rest.
  Invoke-Msys 'true'
  Invoke-Msys 'pacman -Syuu --noconfirm' -MayFail
  Invoke-Msys 'pacman -Syuu --noconfirm'
  $ranPacman = $true
}
if (Test-Path (Join-Path $msys 'usr\bin\pacman.exe')) {
  $lacking = @()
  if (-not (Test-Path (Join-Path $clang64 'clang.exe'))) { $lacking += $packages[0] }
  if (-not (Test-Path (Join-Path $clang64 'mingw32-make.exe'))) { $lacking += $packages[1] }
  if (-not $lacking) { Write-Ok 'MSYS2 CLANG64' "clang and mingw32-make ($clang64)" }
  elseif ($Check) { Write-Missing 'MSYS2 CLANG64' "pacman -S $($lacking -join ' ')" }
  else {
    Write-Installing 'MSYS2 CLANG64' "pacman -S $($lacking -join ' ')"
    # Against the package lists as they are; a list too old to have them is brought up to date, with the system.
    Invoke-Msys "pacman -S --needed --noconfirm $($lacking -join ' ')" -MayFail
    if ($LASTEXITCODE -ne 0) { Invoke-Msys "pacman -Syu --needed --noconfirm $($lacking -join ' ')" }
    $ranPacman = $true
  }
  # pacman's keyring leaves a gpg-agent running from the MSYS2 tree; nothing needs it after the install.
  if ($ranPacman) { Invoke-Msys 'gpgconf --kill all' -MayFail }
  # Last on the PATH, so nothing of MSYS2's shadows Windows' or Git's own tools; MSYS2's usr\bin stays off it, since
  # there a script's `#!/usr/bin/env bash` would find System32's bash.exe, WSL's launcher, ahead of any MSYS bash.
  if (Test-Path $clang64) { Add-ToPath $clang64 -Append }
}

# cgo, for the race detector: on when go env says so and its C compiler exists; else MSYS2's clang, by its full path.
if ($haveGo) {
  $cc = (& go env CC)
  $cgo = (& go env CGO_ENABLED)
  $ccFound = $cc -and (Get-Command $cc -ErrorAction SilentlyContinue)
  $clang = (Join-Path $clang64 'clang.exe') -replace '\\', '/'
  if ($cgo -eq '1' -and $ccFound) { Write-Ok 'cgo (race detector)' "CC=$cc" }
  elseif ($Check) { Write-Missing 'cgo (race detector)' "CGO_ENABLED=$cgo CC=$cc (wants go env -w CC=$clang CGO_ENABLED=1)" }
  elseif (-not (Test-Path $clang)) { Write-Missing 'cgo (race detector)' "no clang at $clang" }
  elseif ($NoPersist) {
    $env:CC = $clang
    $env:CGO_ENABLED = '1'
    Write-Installing 'cgo (race detector)' "CC=$clang CGO_ENABLED=1 (this process only)"
  }
  else {
    go env -w "CC=$clang" CGO_ENABLED=1
    if ($LASTEXITCODE -ne 0) { throw "go env -w exited with $LASTEXITCODE" }
    Write-Installing 'cgo (race detector)' "go env -w CC=$clang CGO_ENABLED=1"
  }
}

Set-GitHooks
Install-VSCodeExtensions

if ($Check) {
  if ($script:missing) { Write-Host "$($script:missing) missing: run tools\setup-dev.ps1 (VS Code: the 'setup environment' task)."; exit 1 }
  Write-Host 'Everything is in place.'
}
else {
  Write-Host 'Done. Open a new terminal (or restart VS Code) for PATH changes to reach it, then, with GOEXPERIMENT=simd:'
  Write-Host '  go test -short ./...     (or, from Git Bash: mingw32-make test-short, mingw32-make race)'
}
