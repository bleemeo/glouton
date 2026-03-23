$versionUrl = "https://packages.bleemeo.com/bleemeo-agent/VERSION"
$baseMsiUrl = "https://packages.bleemeo.com/bleemeo-agent/windows/"
$localPackageName = "Glouton"
$logOutFile = "C:\ProgramData\glouton\auto_update.txt"
$msiLogPath = "C:\ProgramData\glouton\msiexec-log.txt"
$autoUpgradeMarker = "C:\ProgramData\glouton\auto_upgrade"
$logTmpFile = New-TemporaryFile

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

# Function to retrieve the local version
function Get-LocalVersion {
    $package = Get-Package -Name $localPackageName -ErrorAction SilentlyContinue
    if ($package) {
        return $package.Version
    }
    return $null
}

# Function to download the MSI
function Download-MSI {
    param([string]$version)
    $msiName = "glouton_$version.msi"
    $msiUrl = "$baseMsiUrl$msiName"
    $destinationPath = (New-TemporaryFile).FullName
    Invoke-WebRequest -Uri $msiUrl -OutFile $destinationPath
    return $destinationPath
}

# Function to update the package
function Update-Package {
    param([string]$msiPath)
    Start-Process msiexec.exe -ArgumentList "/i `"$msiPath`" /quiet /norestart /l* `"$msiLogPath`"" -Wait
}

# Main logic
function MainLogged {
    Write-Output "Auto update started at $(Get-Date)"

    $remoteVersion = Get-RemoteVersion
    $localVersion = Get-LocalVersion
    Write-Output "Remote version: $remoteVersion"
    Write-Output "Local version: $localVersion"

    if ($remoteVersion -and $localVersion -and ([Version]($remoteVersion) -gt [Version]($localVersion))) {
        Write-Output "New version available. Updating package..."
        New-Item -Path $autoUpgradeMarker
        $msiPath = Download-MSI -version $remoteVersion
        Update-Package -msiPath $msiPath
        Remove-Item $msiPath
        Write-Output "Package updated successfully."
    } elseif ($remoteVersion -and -not $localVersion) {
        Write-Output "Local version not found. Installing package..."
        New-Item -Path $autoUpgradeMarker
        $msiPath = Download-MSI -version $remoteVersion
        Update-Package -msiPath $msiPath
        Remove-Item $msiPath
        Write-Output "Package installed successfully."
    } else {
        Write-Output "No update required. The local version is up to date."
    }

    Write-Output "Auto update finished at $(Get-Date)"
}

function Main {
    try {
        MainLogged | Out-File $logTmpFile
    } catch {
        Add-Content -Path $logTmpFile -Value "An error occurred: $_"
    } finally {
        # Write the log to the output file and make it readable by everyone (for debugging purposes)
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

try {
    Main
} finally {
    Move-Item -Force -Path $logTmpFile -Destination $logOutFile
}
