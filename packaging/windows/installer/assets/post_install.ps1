# This script is executed with admin privileges after installing Glouton.
param(
    [switch]$AutoUpdate,
    [string]$InstallFolder
)

# Define the path and task name
$TaskWindowsUpdateName = "Bleemeo\Glouton\Windows Update Checker"
$WindowsUpdateCheckerLaunch = $( "powershell.exe -ExecutionPolicy Bypass -NonInteractive -File '"  + $InstallFolder + "\windows_update_checker.ps1'" )

$TaskAutoUpdateName = "Bleemeo\Glouton\Auto Update"
$AutoUpdateLaunch = $( "powershell.exe -ExecutionPolicy Bypass -NonInteractive -File '"  + $InstallFolder + "\glouton_auto_update.ps1'" )


# Check if the task already exists
$taskWindowsUpdateCheckerExists = schtasks /Query /TN $TaskWindowsUpdateName 2>$null
if ($?) {
    # If the task exists, delete it
    schtasks /Delete /F /TN $TaskWindowsUpdateName
}

schtasks /Create /F /RU System /SC HOURLY /TN $TaskWindowsUpdateName /TR $WindowsUpdateCheckerLaunch
schtasks /Run /I /TN $TaskWindowsUpdateName

# if the AutoUpdate switch is set, create the task and run it
if ($AutoUpdate) {

    Write-Output "Creating the Auto Update task..."

    # Check if the task already exists
    $taskAutoUpdateExists = schtasks /Query /TN $TaskAutoUpdateName 2>$null
    if ($?) {
        # If the task exists, delete it
        schtasks /Delete /F /TN $TaskAutoUpdateName
    }

    $startTime = (Get-Date).AddDays(1).ToString("HH:mm")
    schtasks /Create /F /RU System /SC DAILY /ST $startTime /TN $TaskAutoUpdateName /TR $AutoUpdateLaunch
    schtasks /Run /I /TN $TaskAutoUpdateName
}

# Make sure all files created in the directory can be read by Glouton.
icacls "C:\ProgramData\glouton" /grant "NT AUTHORITY\LocalService:(OI)(CI)F"