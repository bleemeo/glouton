$remoteUrl = "https://packages.bleemeo.com/bleemeo-agent/windows/"
$msiName = "bleemeo-agent_latest.msi"
$msiUrl = "$remoteUrl$msiName"
$tempPath = "$env:TEMP\$msiName"
$localPackageName = "Glouton"

# Function to retrieve the remote version
# Recall that the Glouton version is defined as : GLOUTON_VERSION=$(date -u +%y.%m.%d.%H%M%S)
# Example : 24.07.15.121603
function Get-RemoteVersion {
    $response = Invoke-WebRequest -Uri $remoteUrl
    if ($response.StatusCode -eq 200) {
        $htmlContent = $response.Content
        $regex = [regex]::Escape($msiName) + ".*?(\d{2}-[A-Za-z]{3}-\d{4} \d{2}:\d{2})"
        if ($htmlContent -match $regex) {
            return (([datetime] $matches[1].Trim()).ToString('yy.MM.dd.HHmmss'))
        }
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
    Invoke-WebRequest -Uri $msiUrl -OutFile $tempPath
}

# Function to update the package
function Update-Package {
    Start-Process msiexec.exe -ArgumentList "/i `"$tempPath`" /quiet /norestart" -Wait
    # msiexec.exe -ArgumentList "/i bleemeo-agent_latest.msi /quiet /norestart" -Wait
    return $null
}

# Main logic
$remoteVersion = Get-RemoteVersion
$localVersion = Get-LocalVersion
Write-Output "Remote version: $remoteVersion"
Write-Output "Local version: $localVersion"

if ($remoteVersion -and $localVersion -and ([Version]($remoteVersion) -gt [Version]($localVersion))) {
    Write-Output "New version available. Updating package..."
    Download-MSI
    Update-Package
    Remove-Item $tempPath
    Write-Output "Package updated successfully."
} else {
    Write-Output "No update required. The local version is up to date."
}