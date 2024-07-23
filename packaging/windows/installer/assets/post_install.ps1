# This script is executed with admin privileges after installing Glouton.
param([string]$InstallFolder)


# Define the paths and tasks names
$WindowsUpdateCheckerScriptPath = "$InstallFolder\windows_update_checker.ps1"
$TaskWindowsUpdateCheckerName = "Bleemeo\Glouton\Windows Update Checker"
$WindowsUpdateCheckerScriptLaucnh = "powershell.exe -ExecutionPolicy Bypass -NonInteractive -File `"$WindowsUpdateCheckerScriptPath`""

$AutoUpdateScriptPath = "$InstallFolder\glouton_auto_update.ps1"
$TaskAutoUpdateName = "Bleemeo\Glouton\Auto Update Task"
$AutoUpdateScriptLaunch = "powershell.exe -ExecutionPolicy Bypass -NonInteractive -File `"$AutoUpdateScriptPath`""

# Check if the tasks already exists
$taskWindowsUpdateCheckerExists = schtasks /Query /TN $TaskWindowsUpdateCheckerName 2>$null
if ($?) {
    # If the task exists, delete it
    schtasks /Delete /F /TN $TaskWindowsUpdateCheckerName
}

$taskAutoUpdateExists = schtasks /Query /TN $TaskAutoUpdateName 2>$null
if ($?) {
    # If the task exists, delete it
    schtasks /Delete /F /TN $TaskAutoUpdateName
}

# Create the scheduled tasks
schtasks /Create /F /RU System /SC HOURLY /TN $TaskWindowsUpdateCheckerName /TR $WindowsUpdateCheckerScriptLaucnh
schtasks /Create /F /RU System /SC HOURLY /TN $TaskAutoUpdateName /TR $AutoUpdateScriptLaunch

# Run the scheduled tasks immediately
schtasks /Run /I /TN $TaskWindowsUpdateCheckerName
schtasks /Run /I /TN $TaskAutoUpdateName

# Make sure all files created in the directory can be read by Glouton.
icacls "C:\ProgramData\glouton" /grant "NT AUTHORITY\LocalService:(OI)(CI)F"