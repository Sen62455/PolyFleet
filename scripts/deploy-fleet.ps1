[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern("^v[0-9A-Za-z][0-9A-Za-z._-]*$")]
    [string]$Version,

    [string]$ConfigPath = (Join-Path $PSScriptRoot "fleet.local.psd1")
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function Invoke-Checked {
    param(
        [Parameter(Mandatory)]
        [string]$Command,
        [Parameter(ValueFromRemainingArguments)]
        [string[]]$Arguments
    )
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Command exited with code $LASTEXITCODE"
    }
}

foreach ($commandName in @("gh", "ssh", "scp")) {
    if (-not (Get-Command $commandName -ErrorAction SilentlyContinue)) {
        throw "Required command is missing: $commandName"
    }
}

$resolvedConfigPath = (Resolve-Path -LiteralPath $ConfigPath).Path
$fleet = Import-PowerShellDataFile -LiteralPath $resolvedConfigPath
if ($fleet.Repository -notmatch "^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$" -or
    $fleet.Architecture -notin @("amd64", "arm64") -or
    -not $fleet.Nodes -or $fleet.Nodes.Count -gt 32) {
    throw "Fleet configuration has an invalid repository, architecture, or node list"
}

foreach ($node in $fleet.Nodes) {
    if ($node.Name -notmatch "^[A-Za-z0-9._-]{1,64}$" -or
        $node.Target -notmatch "^[A-Za-z0-9._-]+@[A-Za-z0-9._:\[\]-]+$" -or
        [int]$node.Port -lt 1 -or [int]$node.Port -gt 65535 -or
        -not $node.Components -or $node.Components.Count -gt 2) {
        throw "Fleet node configuration is invalid: $($node.Name)"
    }
    foreach ($component in $node.Components) {
        if ($component -notin @("server", "agent")) {
            throw "Unsupported component '$component' for $($node.Name)"
        }
    }
    if ($node.ContainsKey("RequiredEnvironment")) {
        if ($node.RequiredEnvironment.Count -gt 16) {
            throw "Too many required environment variables for $($node.Name)"
        }
        foreach ($variableName in $node.RequiredEnvironment) {
            if ([string]$variableName -notmatch "^[A-Z_][A-Z0-9_]{0,63}$") {
                throw "Invalid required environment variable for $($node.Name)"
            }
        }
    }
    if ($node.ContainsKey("IdentityFile") -and $node.IdentityFile) {
        $node.IdentityFile = (Resolve-Path -LiteralPath $node.IdentityFile).Path
    }
}

Invoke-Checked gh auth status --hostname github.com

$packageName = "polyfleet-$Version-linux-$($fleet.Architecture)"
$archiveName = "$packageName.tar.gz"
$checksumName = "$archiveName.sha256"
$temporaryRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("polyfleet-deploy-" + [guid]::NewGuid().ToString("N"))
$resolvedTemporaryParent = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
New-Item -ItemType Directory -Path $temporaryRoot | Out-Null

try {
    Invoke-Checked gh release download $Version --repo $fleet.Repository `
        --pattern $archiveName --pattern $checksumName --dir $temporaryRoot

    $archivePath = Join-Path $temporaryRoot $archiveName
    $checksumPath = Join-Path $temporaryRoot $checksumName
    if (-not (Test-Path -LiteralPath $archivePath) -or -not (Test-Path -LiteralPath $checksumPath)) {
        throw "GitHub Release does not contain the expected $($fleet.Architecture) assets"
    }
    $checksumLine = (Get-Content -Raw -LiteralPath $checksumPath).Trim()
    $checksumPattern = "^([0-9a-fA-F]{64})\s+\*?" + [regex]::Escape($archiveName) + "$"
    if ($checksumLine -notmatch $checksumPattern) {
        throw "Release checksum file has an invalid format"
    }
    $expectedHash = $Matches[1].ToLowerInvariant()
    $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
    if ($actualHash -ne $expectedHash) {
        throw "Downloaded release checksum mismatch"
    }

    $jobScript = {
        param(
            $Node,
            $ArchivePath,
            $ChecksumPath,
            $ArchiveName,
            $ChecksumName,
            $PackageName,
            $Component
        )
        $ErrorActionPreference = "Stop"
        Set-StrictMode -Version Latest

        function Invoke-NativeChecked {
            param([string]$Command, [string[]]$Arguments)
            & $Command @Arguments
            if ($LASTEXITCODE -ne 0) {
                throw "$Command exited with code $LASTEXITCODE"
            }
        }

        $sshArguments = @(
            "-o", "BatchMode=yes",
            "-o", "StrictHostKeyChecking=yes",
            "-p", [string]$Node.Port
        )
        $scpArguments = @(
            "-o", "BatchMode=yes",
            "-o", "StrictHostKeyChecking=yes",
            "-P", [string]$Node.Port
        )
        if ($Node.ContainsKey("IdentityFile") -and $Node.IdentityFile) {
            $sshArguments += @("-i", [string]$Node.IdentityFile)
            $scpArguments += @("-i", [string]$Node.IdentityFile)
        }
        $target = [string]$Node.Target
        $remoteDirectory = "/tmp/polyfleet-deploy-" + [guid]::NewGuid().ToString("N")
        if ($remoteDirectory -notmatch "^/tmp/polyfleet-deploy-[0-9a-f]{32}$") {
            throw "Generated remote directory is invalid"
        }
        try {
            Write-Output "[$($Node.Name)] preparing $PackageName ($Component)"
            Invoke-NativeChecked ssh ($sshArguments + @($target, "install -d -m 0700 '$remoteDirectory'"))
            Invoke-NativeChecked scp ($scpArguments + @($ArchivePath, "${target}:${remoteDirectory}/"))
            Invoke-NativeChecked scp ($scpArguments + @($ChecksumPath, "${target}:${remoteDirectory}/"))

            $commands = @(
                "set -Eeuo pipefail",
                'run_as_root() { if [ "${EUID}" -eq 0 ]; then "$@"; else sudo -n "$@"; fi; }',
                "cd '$remoteDirectory'",
                "sha256sum -c '$ChecksumName'",
                "tar -xzf '$ArchiveName'",
                "cd '$PackageName'",
                "sha256sum -c SHA256SUMS",
                "bash -n deploy/*.sh"
            )
            if ($Component -eq "agent" -and $Node.ContainsKey("RequiredEnvironment")) {
                foreach ($variableName in $Node.RequiredEnvironment) {
                    $commands += "run_as_root grep -Eq '^$variableName=[^[:space:]]+$' /etc/hyfleet/agent.env"
                }
            }
            $commands += "run_as_root bash deploy/update-component.sh '$Component'"
            Invoke-NativeChecked ssh ($sshArguments + @($target, ($commands -join "; ")))
            Write-Output "[$($Node.Name)] $Component update completed"
        }
        finally {
            & ssh @sshArguments $target "rm -rf -- '$remoteDirectory'" 2>$null | Out-Null
        }
    }

    foreach ($component in @("server", "agent")) {
        $phaseNodes = @($fleet.Nodes | Where-Object { $_.Components -contains $component })
        if ($phaseNodes.Count -eq 0) {
            continue
        }
        Write-Host "Starting $component update phase for $($phaseNodes.Count) node(s)."
        $jobs = @()
        foreach ($node in $phaseNodes) {
            $jobs += Start-Job -Name "$($node.Name)-$component" -ScriptBlock $jobScript -ArgumentList @(
                $node, $archivePath, $checksumPath, $archiveName, $checksumName,
                $packageName, $component
            )
        }
        Wait-Job -Job $jobs | Out-Null
        $failures = @()
        foreach ($job in $jobs) {
            try {
                Receive-Job -Job $job -ErrorAction Stop | ForEach-Object { Write-Host $_ }
                if ($job.State -ne "Completed") {
                    throw ($job.ChildJobs[0].JobStateInfo.Reason.Message)
                }
            }
            catch {
                $failures += "$($job.Name): $($_.Exception.Message)"
            }
            finally {
                Remove-Job -Job $job -Force
            }
        }
        if ($failures.Count -gt 0) {
            throw ("Fleet $component update phase failed:`n" + ($failures -join "`n"))
        }
        Write-Host "Completed $component update phase."
    }
    Write-Host "All $($fleet.Nodes.Count) nodes now run $Version."
}
finally {
    $resolvedTemporaryRoot = [System.IO.Path]::GetFullPath($temporaryRoot)
    if ($resolvedTemporaryRoot.StartsWith($resolvedTemporaryParent, [System.StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path -Leaf $resolvedTemporaryRoot) -match "^polyfleet-deploy-[0-9a-f]{32}$" -and
        (Test-Path -LiteralPath $resolvedTemporaryRoot)) {
        Remove-Item -LiteralPath $resolvedTemporaryRoot -Recurse -Force
    }
}
