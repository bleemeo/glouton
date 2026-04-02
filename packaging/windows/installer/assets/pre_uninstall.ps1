# This script is executed with admin privileges before uninstallation.
Start-Transcript -Path "C:\ProgramData\glouton\logs\last_pre_uninstall.log" -ErrorAction Continue

Write-Output "Pre-uninstall start"

# Delete update checker task.
schtasks /Delete /F /TN "Bleemeo\Glouton\Windows Update Checker"
schtasks /Delete /F /TN "Bleemeo\Glouton\Auto Upgrade"
schtasks /Delete /F /TN "Bleemeo\Glouton\Auto Update"

Write-Output "Pre-uninstall finished"

Stop-Transcript
