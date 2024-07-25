param([string]$InstallFolder)

$AutoUpdateScriptPath = "$InstallFolder\glouton_auto_update.ps1"
$TaskAutoUpdateName = "Bleemeo\Glouton\Auto Update Task"
$AutoUpdateScriptLaunch = "powershell.exe -ExecutionPolicy Bypass -NonInteractive -File `"$AutoUpdateScriptPath`""

$taskAutoUpdateExists = schtasks /Query /TN $TaskAutoUpdateName 2>$null
if ($?) {
    # If the task exists, delete it
    schtasks /Delete /F /TN $TaskAutoUpdateName
}

# Calculate the start time for the Auto Update task to be 24 hours from now
$startTime = (Get-Date).AddDays(1).ToString("HH:mm")
schtasks /Create /F /RU System /SC DAILY /ST $startTime /TN $TaskAutoUpdateName /TR $AutoUpdateScriptLaunch

$versionUrl = "https://packages.bleemeo.com/bleemeo-agent/VERSION"
$baseMsiUrl = "https://packages.bleemeo.com/bleemeo-agent/windows/"
$localPackageName = "Glouton"
$tempPath = [System.IO.Path]::GetTempPath()

# Function to retrieve the remote version
function Get-RemoteVersion {
    $response = Invoke-WebRequest -Uri $versionUrl
    if ($response.StatusCode -eq 200) {
        return $response.Content.Trim()
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
    $destinationPath = [System.IO.Path]::Combine($tempPath, $msiName)
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
