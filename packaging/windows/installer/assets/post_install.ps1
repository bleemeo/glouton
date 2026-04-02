# This script is executed with admin privileges after installing Glouton.
param(
    [switch]$AutoUpdate,
    [string]$InstallFolder
)

Start-Transcript -Path "C:\ProgramData\glouton\logs\last_post_install.log" -ErrorAction Continue

# Define the path and task name
$TaskWindowsUpdateName = "Bleemeo\Glouton\Windows Update Checker"
$WindowsUpdateCheckerLaunch = $( "powershell.exe -ExecutionPolicy Bypass -NonInteractive -File '"  + $InstallFolder + "\windows_update_checker.ps1'" )

$OldTaskAutoUpdateName = "Bleemeo\Glouton\Auto Update"
$TaskAutoUpdateName = "Bleemeo\Glouton\Auto Upgrade"
$AutoUpdateLaunch = $( "powershell.exe -ExecutionPolicy Bypass -NonInteractive -File '"  + $InstallFolder + "\glouton_auto_upgrade.ps1'" )
$logAutoUpdateFile = "C:\ProgramData\glouton\logs\auto_upgrade.log"

Write-Output "Post-install start with args \"$AutoUpdate\" and \"$InstallFolder\""

schtasks /Create /F /RU System /SC HOURLY /TN $TaskWindowsUpdateName /TR $WindowsUpdateCheckerLaunch
schtasks /Run /I /TN $TaskWindowsUpdateName

schtasks /Query /TN $OldTaskAutoUpdateName 2>$null
if ($?) {
    schtasks /Delete /F /TN $OldTaskAutoUpdateName
}

# if the AutoUpdate switch is set, create the task and run it
if ($AutoUpdate) {

    Write-Output "Creating the Auto Update task..."
    
    # Create the log file for auto updates
    # The presence of this file indicates that the auto update has been enabled
    if (-not (Test-Path $logAutoUpdateFile)) {
        New-Item -Path $logAutoUpdateFile -ErrorAction Continue
    }

    $startTime = (Get-Date).ToString("HH:mm")
    schtasks /Create /F /RU System /SC DAILY /ST $startTime /TN $TaskAutoUpdateName /TR $AutoUpdateLaunch
} else {
    # If the auto update is disabled, remove the log file
    Remove-Item -Path $logAutoUpdateFile -Force -ErrorAction SilentlyContinue

    schtasks /Query /TN $TaskAutoUpdateName 2>$null
    if ($?) {
        schtasks /Change /TN $TaskAutoUpdateName /DISABLE
    }
}

# Make sure all files created in the directory can be read by Glouton.
icacls "C:\ProgramData\glouton" /grant "NT AUTHORITY\LocalService:(OI)(CI)F"

Write-Output "Post-install finished"

Stop-Transcript
