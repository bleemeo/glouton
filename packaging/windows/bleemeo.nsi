# Rewrite modernui block to support the following change:
# * Add our custom page after the welcome page (to ask account id and registration key)
# * Add a confirmation on uninstall.
# * Change the installer icon.
!include "MUI2.nsh"
!include LogicLib.nsh
!include FileFunc.nsh

Name "glouton"
OutFile "glouton-installer.exe"
Unicode True

InstallDir "$PROGRAMFILES\bleemeo\glouton"

!define PRODUCT_ICON "bleemeo.ico"
!define PRODUCT_NAME "glouton"
!define CONFIGDIR "C:\ProgramData\bleemeo\glouton"

!define MUI_ABORTWARNING
!define MUI_ICON ${PRODUCT_ICON}
!define MUI_UNICON ${PRODUCT_ICON}
!define MUI_WELCOMEFINISHPAGE_BITMAP ../../packaging/windows/bleemeo_logo.bmp

!insertmacro MUI_PAGE_WELCOME
Page custom informationPage informationPageLeave
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

!insertmacro GetParameters
!insertmacro GetOptions

!include LogicLib.nsh
Var AccountIDTextBox
Var RegistrationKeyTextBox
Var AccountIDValue
Var RegistrationKeyValue

Section "!${PRODUCT_NAME}"
  SetOutPath "$INSTDIR"

  # Stopping the service is required before try to overwrite its file, if it is already installed
  ExecShellWait "sc.exe" "stop glouton" SW_HIDE

  File ../../dist/glouton_windows_amd64/glouton.exe
  File ${PRODUCT_ICON}

  WriteUninstaller "$INSTDIR\Uninstall.exe"

  # Let's expose proper values in the "Apps & Features" Windows settings by registering our installer
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "DisplayName" "glouton by Bleemeo -- Easy Monitoring for Scalable Infrastructure"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "UninstallString" "$\"$INSTDIR\Uninstall.exe$\""
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "RegOwner" "Bleemeo"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "RegCompany" "Bleemeo"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "Publisher" "Bleemeo"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "InstallLocation" "$\"$INSTDIR$\""
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "DisplayIcon" "$\"$INSTDIR\${PRODUCT_ICON}$\""
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "NoModify" 1
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "NoRepair" 1
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "URLInfoAbout" "https://bleemeo.com/"

  # Delete stray versions of the service
  ExecWait 'sc.exe query glouton' $0
  DetailPrint $0
  # the service already exists -> let's delete it, and re-install it, in case the config changed
  ${If} $0 == 0
    ExecWait 'sc.exe delete glouton'
  ${EndIf}

  # 
  ExecWait 'sc.exe create glouton binPath="$INSTDIR\glouton.exe" type=own start=auto DisplayName="Glouton by Bleemeo -- Monitoring Agent"' $0
  DetailPrint $0
  ${If} $0 != 0
    MessageBox MB_OK "Service installation failed. You may consider restarting this machine, in case there is ongoing windows updates or services changes, and restarting this installer. If this happens again, please report us the issue at support@bleemeo.com." 
    Abort "The installation of the Windows service failed !"
  ${EndIf}

  # do not overwrite the config file
  SetOverwrite off

  CreateDirectory "${CONFIGDIR}"

  SetOverwrite on

  # Compute the folder size to tell windows about the approximate size of glouton
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "EstimatedSize" "$0"

  # Start the service
  ExecWait 'sc.exe start glouton'
SectionEnd


Section "Uninstall"
  # Unregister our uninstaller
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton"

  ExecWait  'sc.exe delete glouton'

  RMDir /r "$INSTDIR"
  # delete the bleemeo folder too if it is empty
  RMDir "$PROGRAMFILES\bleemeo"
SectionEnd


# Create the content of our custom page.
Function informationPage

    # Only show this page if it's the first installation. After first installation, the file
    # 30-install.conf will be created.
    IfFileExists "${CONFIGDIR}\glouton.conf.d\30-install.conf" NotFirstInstall FirstInstall
    NotFirstInstall:
    Abort
    FirstInstall:

    !insertmacro MUI_HEADER_TEXT "Configure your agent" "Enter the credentials for communication with Bleemeo"

    nsDialogs::Create 1018
    Pop $0
    ${If} $0 == error
        Abort
    ${EndIf}

    # Dialog is 300 unit width and 140 unit high.
    ${NSD_CreateLabel} 0 0 100% 12u "Enter your account ID"
    Pop $0

    ${NSD_CreateText} 0 13u 100% 12u $AccountIDValue
    Pop $AccountIDTextBox

    ${NSD_CreateLabel} 0 27u 100% 12u "Enter your registration key"
    Pop $0

    ${NSD_CreateText} 0 40u 100% 12u $RegistrationKeyValue
    Pop $RegistrationKeyTextBox

    nsDialogs::Show
FunctionEnd

Function informationPageLeave
    ${NSD_GetText} $AccountIDTextBox $AccountIDValue
    ${NSD_GetText} $RegistrationKeyTextBox $RegistrationKeyValue
FunctionEnd


Function .onInit
    ${GetParameters} $R0
    ClearErrors
    ${GetOptions} $R0 /ACCOUNT= $AccountIDValue
    ${GetOptions} $R0 /REGKEY= $RegistrationKeyValue
FunctionEnd
