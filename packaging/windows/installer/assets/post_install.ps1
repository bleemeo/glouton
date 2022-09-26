# This script is executed with admin privileges after installing Glouton.
param([string]$InstallFolder)

# Create and run the update checker service.
$UpdateChecker = $( "powershell.exe -ExecutionPolicy Bypass -NonInteractive -File '"  + $InstallFolder + "\windows_update_checker.ps1'" )
schtasks /Create /F /RU System /SC HOURLY /TN "Bleemeo\Glouton\Windows Update Checker" /TR $UpdateChecker
schtasks /Run /I /TN "Bleemeo\Glouton\Windows Update Checker"

# Make sure all files created in the directory can be read by Glouton.
icacls "C:\ProgramData\glouton" /grant "NT AUTHORITY\LocalService:(OI)(CI)F"
