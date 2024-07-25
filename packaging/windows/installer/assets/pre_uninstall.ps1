# This script is executed with admin privileges before uninstallation.

# Delete update checker task.
schtasks /Delete /F /TN "Bleemeo\Glouton\Windows Update Checker"
schtasks /Delete /F /TN "Bleemeo\Glouton\Auto Update Task"
