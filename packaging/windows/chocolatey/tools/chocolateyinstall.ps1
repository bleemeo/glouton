$ErrorActionPreference = 'Stop';

$packageName= 'glouton'
$toolsDir   = "$(Split-Path -parent $MyInvocation.MyCommand.Definition)"
$fileLocation = Join-Path $toolsDir 'glouton.exe'

$packageArgs = @{
  packageName   = $packageName
  fileType      = 'msi'
  file         = $fileLocation
  softwareName  = 'Glouton*' #part or all of the Display Name as you see it in Programs and Features. It should be enough to be unique
  silentArgs    = "/qn /norestart /l*v `"$($env:TEMP)\$($packageName).$($env:chocolateyPackageVersion).MsiInstall.log`"" # ALLUSERS=1 DISABLEDESKTOPSHORTCUT=1 ADDDESKTOPICON=0 ADDSTARTMENU=0
  validExitCodes= @(0, 3010, 1641)
}

Install-ChocolateyInstallPackage @packageArgs
