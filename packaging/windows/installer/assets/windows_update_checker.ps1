# Write the number of pending upgrades to a file.

$UpdateSession = New-Object -ComObject "Microsoft.Update.Session"
$UpdateSearcher = $UpdateSession.CreateUpdateSearcher()

$Updates = $UpdateSearcher.Search("IsInstalled = 0 and IsHidden = 0").Updates.Count
$SecurityUpdates = $UpdateSearcher.Search("(IsInstalled = 0 and IsHidden = 0 and CategoryIDs contains 'E6CF1350-C01B-414D-A61F-263D14D133B4') or (IsInstalled = 0 and IsHidden = 0 and CategoryIDs contains '0FA1201D-4330-4FA8-8AE9-B877473B6441')").Updates.Count

$TmpFile = New-TemporaryFile
$OutFile = "C:\ProgramData\glouton\windowsupdate.txt"

$Updates | Out-File -FilePath $TmpFile -Encoding ascii
Add-Content -Path $TmpFile -Value $SecurityUpdates

$acl = Get-Acl $TmpFile
$perms = "NT AUTHORITY\LocalService", "Read", "None", "None", "Allow"
$rule = New-Object -TypeName System.Security.AccessControl.FileSystemAccessRule -ArgumentList $perms
$acl.SetAccessRule($rule)
$acl | Set-Acl -Path $TmpFile

Move-Item -Force -Path $TmpFile -Destination $OutFile
