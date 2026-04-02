$versionUrl = "https://packages.bleemeo.com/bleemeo-agent/VERSION"
$baseMsiUrl = "https://packages.bleemeo.com/bleemeo-agent/windows/"
$localPackageName = "Glouton"

$deprecatedLogFile = "C:\ProgramData\glouton\auto_update.txt"
$deprecatedmsiLogPath = "C:\ProgramData\glouton\msiexec-log.txt"
$deprecatedAutoUpgradeMarker = "C:\ProgramData\glouton\auto_update"

$logOutFile = "C:\ProgramData\glouton\logs\auto_upgrade.log"
$logOutFileOld = "C:\ProgramData\glouton\logs\auto_upgrade_old.log"
$msiLogPath = "C:\ProgramData\glouton\logs\msiexec.log"
$msiLogPathOld = "C:\ProgramData\glouton\logs\msiexec_old.log"
# This is the marker file that is used by Glouton to known it is shutdown for an auto_upgrade.
$autoUpgradeMarker = "C:\ProgramData\glouton\auto_upgrade"

# Function to retrieve the remote version
function Get-RemoteVersion {
    $response = Invoke-WebRequest -UseBasicParsing -ContentType "text/plain" -Uri $versionUrl

    if ($response.StatusCode -eq 200) {
        if ($response.Content.GetType() -eq [System.Byte[]]) {
            $content_text = [System.Text.Encoding]::UTF8.GetString($response.Content)
        } else {
            $content_text = $response.Content
        }
        return $content_text.Trim()
    }

    return $null
}

function New-TemporaryDirectory {
    $parent = [System.IO.Path]::GetTempPath()
    $count = 0
    do {
        $count = $count + 1
        $name = [System.IO.Path]::GetRandomFileName()
        $item = New-Item -Path $parent -Name $name -ItemType "directory" -ErrorAction SilentlyContinue
    } while (-not $item -and $count -lt 2)

    if (-not $item) {
        throw "unable to create a temporary folder after $count tries"
    }

    return $item.FullName
}

# Function to retrieve the local version
function Get-LocalVersion {
    $package = Get-Package -Name $localPackageName -ErrorAction Continue
    if ($package) {
        return $package.Version
    }
    return $null
}

# Function to download the MSI
function Download-MSI {
    param([string]$version, [string]$workDir)
    $msiName = "glouton_$version.msi"
    $msiUrl = "$baseMsiUrl$msiName"
    $destinationPath = (Join-Path $workDir $msiName)
    Invoke-WebRequest -Uri $msiUrl -OutFile $destinationPath
    return $destinationPath
}

# Function to update the package
function Update-Package {
    param([string]$msiPath)

    if (Test-Path $msiLogPath) {
        Move-Item -Force -Path $msiLogPath -Destination $msiLogPathOld
    }

    $proc = Start-Process msiexec.exe -ArgumentList "/i `"$msiPath`" /quiet /norestart /l*v `"$msiLogPath`"" -Wait -PassThru
    if ($proc.ExitCode -ne 0) {
        throw "msiexec failed with exit code $($proc.ExitCode)"
    }
}

# Main logic
function MainLogged {
    param([string]$workTempDir)
    Write-Output "Auto upgrade started at $(Get-Date)"

    $remoteVersion = Get-RemoteVersion
    $localVersion = Get-LocalVersion
    Write-Output "Remote version: $remoteVersion"
    Write-Output "Local version: $localVersion"

    $need_update = $false

    if ($remoteVersion -and $localVersion -and ([Version]($remoteVersion) -gt [Version]($localVersion))) {
        Write-Output "New version available. Updating package..."
        $need_update = $true
    } elseif ($remoteVersion -and -not $localVersion) {
        Write-Output "Local version not found. Installing package..."
        $need_update = $true
    } else {
        Write-Output "No upgrade required. The local version is up to date."
    }

    if ($need_update) {
        New-Item -Path $autoUpgradeMarker
        $msiPath = Download-MSI -version $remoteVersion -workDir $workTempDir
        try {
            Update-Package -msiPath $msiPath
            Write-Output "Package upgraded successfully."
        } catch {
            Write-Output "Installation / upgrade failed: $_"
        } finally {
            Remove-Item $msiPath -ErrorAction Continue
        }
    }

    Write-Output "Auto upgrade finished at $(Get-Date)"
}

function HandleDeprecatedLogs {
    if (Test-Path $deprecatedLogFile) {
        if (Test-Path $logOutFile) {
            Remove-Item $deprecatedLogFile -ErrorAction Continue
        } else {
            Move-Item -Force -Path $deprecatedLogFile -Destination $logOutFile -ErrorAction Continue
        }
    }

    if (Test-Path $deprecatedmsiLogPath) {
        if (Test-Path $msiLogPath) {
            Remove-Item $deprecatedmsiLogPath -ErrorAction Continue
        } else {
            Move-Item -Force -Path $deprecatedmsiLogPath -Destination $msiLogPath -ErrorAction Continue
        }
    }

    if (Test-Path $deprecatedAutoUpgradeMarker) {
        Remove-Item $deprecatedAutoUpgradeMarker -ErrorAction Continue
    }
}

function Main {
    param([string]$workTempDir, [string]$logTmpFile)

    try {
        Start-Transcript -Path $logTmpFile

        echo "Starting script at $(Get-Date  -Format "yyyy-MM-dd HH:mm K")"
        
        HandleDeprecatedLogs

        MainLogged $workTempDir

        echo "-- Content of pre_uninstall.log"
        Get-Content -Path "C:\ProgramData\glouton\logs\last_pre_uninstall.log" -ErrorAction SilentlyContinue
        
        echo "-- Content of post_install.log"
        Get-Content -Path "C:\ProgramData\glouton\logs\last_post_install.log" -ErrorAction SilentlyContinue

        echo "-- Troubleshoot help"
        whoami /all
    } catch {
        Write-Error "An error occurred: $_"
    } finally {
        Stop-Transcript

        # Write the log to the output file and make it readable by everyone (for debugging purposes)
        if (Test-Path $logTmpFile) {
            $acl = Get-Acl $logTmpFile
            $everyoneSid = New-Object System.Security.Principal.SecurityIdentifier "S-1-1-0"
            $rights = [System.Security.AccessControl.FileSystemRights]::Read
            $inheritanceFlags = [System.Security.AccessControl.InheritanceFlags]::None
            $propagationFlags = [System.Security.AccessControl.PropagationFlags]::None
            $type = [System.Security.AccessControl.AccessControlType]::Allow
            $rule = New-Object -TypeName System.Security.AccessControl.FileSystemAccessRule -ArgumentList $everyoneSid, $rights, $inheritanceFlags, $propagationFlags, $type
            $acl.SetAccessRule($rule)
            $acl | Set-Acl -Path $logTmpFile
        }
    }
}

try {
    $workTempDir = New-TemporaryDirectory
    $logTmpFile = (Join-Path $workTempDir "auto-upgrade.log")

    Main $workTempDir $logTmpFile 
} finally {
    if (Test-Path $logTmpFile) {
        if (Test-Path $logOutFile) {
            Move-Item -Force -Path $logOutFile -Destination $logOutFileOld
        }

        Move-Item -Force -Path $logTmpFile -Destination $logOutFile
    }

    Remove-Item -Path $logTmpFile -ErrorAction SilentlyContinue
    Remove-Item -Path $workTempDir -Recurse -ErrorAction Continue
}
