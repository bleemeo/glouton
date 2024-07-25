# This script is executed with admin privileges after installing Glouton.
param([string]$InstallFolder)

# Define the path and task name
$WindowsUpdateCheckerScriptPath = "$InstallFolder\windows_update_checker.ps1"
$TaskWindowsUpdateCheckerName = "Bleemeo\Glouton\Windows Update Checker"
$WindowsUpdateCheckerScriptLaucnh = "powershell.exe -ExecutionPolicy Bypass -NonInteractive -File `"$WindowsUpdateCheckerScriptPath`""

# Check if the task already exists
$taskWindowsUpdateCheckerExists = schtasks /Query /TN $TaskWindowsUpdateCheckerName 2>$null
if ($?) {
    # If the task exists, delete it
    schtasks /Delete /F /TN $TaskWindowsUpdateCheckerName
}

# Create the task and run it
schtasks /Create /F /RU System /SC HOURLY /TN $TaskWindowsUpdateCheckerName /TR $WindowsUpdateCheckerScriptLaucnh
schtasks /Run /I /TN $TaskWindowsUpdateCheckerName

# Make sure all files created in the directory can be read by Glouton.
icacls "C:\ProgramData\glouton" /grant "NT AUTHORITY\LocalService:(OI)(CI)F"