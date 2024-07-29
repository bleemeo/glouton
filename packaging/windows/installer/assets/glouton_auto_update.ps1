$versionUrl = "https://packages.bleemeo.com/bleemeo-agent/VERSION"
$baseMsiUrl = "https://packages.bleemeo.com/bleemeo-agent/windows/"
$localPackageName = "Glouton"
$tempPath = [System.IO.Path]::GetTempPath()
$logOutFile = "C:\ProgramData\glouton\auto_update.txt"
$logTmpFile = New-TemporaryFile
Out-File -FilePath $logTmpFile -Encoding ascii
Add-Content -Path $logTmpFile -Value "Auto update started at $(Get-Date)"

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
    Start-Process msiexec.exe -ArgumentList "/i `"$msiPath`" /quiet /norestart" -Wait
}

# Main logic
$remoteVersion = Get-RemoteVersion
$localVersion = Get-LocalVersion
Write-Output "Remote version: $remoteVersion"
Add-Content -Path $logTmpFile -Value "Remote version: $remoteVersion"
Write-Output "Local version: $localVersion"
Add-Content -Path $logTmpFile -Value "Local version: $localVersion"

if ($remoteVersion -and $localVersion -and ([Version]($remoteVersion) -gt [Version]($localVersion))) {
    Write-Output "New version available. Updating package..."
    Add-Content -Path $logTmpFile -Value "New version available. Updating package..."
    $msiPath = Download-MSI -version $remoteVersion
    Update-Package -msiPath $msiPath
    Remove-Item $msiPath
    Write-Output "Package updated successfully."
    Add-Content -Path $logTmpFile -Value "Package updated successfully."
} elseif ($remoteVersion -and -not $localVersion) {
    Write-Output "Local version not found. Installing package..."
    Add-Content -Path $logTmpFile -Value "Local version not found. Installing package..."
    $msiPath = Download-MSI -version $remoteVersion
    Update-Package -msiPath $msiPath
    Remove-Item $msiPath
    Write-Output "Package installed successfully."
    Add-Content -Path $logTmpFile -Value "Package installed successfully."
} else {
    Write-Output "No update required. The local version is up to date."
    Add-Content -Path $logTmpFile -Value "No update required. The local version is up to date."
}

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

Move-Item -Force -Path $logTmpFile -Destination $logOutFile
