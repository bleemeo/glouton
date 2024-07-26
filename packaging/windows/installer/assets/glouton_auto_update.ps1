$versionUrl = "https://gist.githubusercontent.com/Minifixio/dba71027a387c9f33a3246fcad3b2cf7/raw/328e7d70ad194325eb60a1e564d7b15037561ded/VERSION.txt"
$baseMsiUrl = "https://packages.bleemeo.com/bleemeo-agent/windows/"
$localPackageName = "Glouton"
$tempPath = [System.IO.Path]::GetTempPath()

# Function to retrieve the remote version
function Get-RemoteVersion {
     $response = Invoke-WebRequest -UseBasicParsing -ContentType "text/plain" -Uri $versionUrl

    if ($response.StatusCode -eq 200) {
        $content_text = $response.Content
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
Write-Output "Local version: $localVersion"

if ($remoteVersion -and $localVersion -and ([Version]($remoteVersion) -gt [Version]($localVersion))) {
    Write-Output "New version available. Updating package..."
    $msiPath = Download-MSI -version $remoteVersion
    Update-Package -msiPath $msiPath
    Remove-Item $msiPath
    Write-Output "Package updated successfully."
} elseif ($remoteVersion -and -not $localVersion) {
    Write-Output "Local version not found. Installing package..."
    $msiPath = Download-MSI -version $remoteVersion
    Update-Package -msiPath $msiPath
    Remove-Item $msiPath
    Write-Output "Package installed successfully."
} else {
    Write-Output "No update required. The local version is up to date."
}
