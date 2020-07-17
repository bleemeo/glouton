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
!define CONFIGDIR "C:\ProgramData\glouton"
!define PRODUCT_VERSION "0.1"
!define AGENT_SERVICE_NAME "bleemeo-glouton-agent"

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

  # Create config directories
  CreateDirectory "${CONFIGDIR}"
  CreateDirectory "${CONFIGDIR}\glouton.conf.d"

  #### CLEANUP ####

  # Delete stray versions of the service
  nsExec::ExecToLog 'sc.exe query "${AGENT_SERVICE_NAME}"'
  Pop $0
  # the service already exists -> let's delete it, and re-install it, in case the config changed
  ${If} $0 == 0
    # Stopping the service is required before try to overwrite its file, if it is already installed
    nsExec::ExecToLog 'sc.exe stop "${AGENT_SERVICE_NAME}"'
    nsExec::ExecToLog 'sc.exe delete "${AGENT_SERVICE_NAME}"'
  ${EndIf}

  #### UPGRADE FROM BLEEMEO-AGENT ####

  # Delete bleemeo agent if present
  IfFileExists "$PROGRAMFILES\bleemeo-agent" 0 old_agent_not_present

  # Move its config & state
  SetOverwrite off
  CopyFiles "C:\ProgramData\bleemeo\etc\agent.conf" "${CONFIGDIR}\glouton.conf"
  CopyFiles "C:\ProgramData\bleemeo\etc\agent.conf.d\*.conf" "${CONFIGDIR}\glouton.conf.d\"
  CopyFiles "C:\ProgramData\bleemeo\state.json" "${CONFIGDIR}"
  SetOverwrite on

  # Uninstall the bleemeo-agent
  ExecWait '"$PROGRAMFILES\bleemeo-agent\uninstall.exe" /S'

old_agent_not_present:

  #### INSTALLATION ####
  File ../../dist/glouton_windows_amd64/glouton.exe
  # Needed for the icon shown in 'Apps & Features'
  File ${PRODUCT_ICON}

  WriteUninstaller "$INSTDIR\Uninstall.exe"

  IfFileExists "${CONFIGDIR}\glouton.conf.d\30-install.conf" +4 0
  # generate the config file with the account ID and registration ID
  File gen_config.exe
  nsExec::ExecToLog '"$INSTDIR\gen_config.exe" --account "$AccountIDValue" --key "$RegistrationKeyValue"'
  Delete gen_config.exe

  # Let's expose proper values in the "Apps & Features" Windows settings by registering our installer
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "DisplayName" "glouton by Bleemeo -- Easy Monitoring for Scalable Infrastructure"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "UninstallString" "$\"$INSTDIR\Uninstall.exe$\""
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "DisplayVersion" "${PRODUCT_VERSION}"
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

  # Compute the folder size to tell windows about the approximate size of glouton
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton" \
                   "EstimatedSize" "$0"

  # Create the service
  nsExec::ExecToLog 'sc.exe create "${AGENT_SERVICE_NAME}" binPath="$INSTDIR\glouton.exe" type=own start=auto DisplayName="Glouton by Bleemeo -- Monitoring Agent"'
  Pop $0
  ${If} $0 != 0
    MessageBox MB_OK "Service installation failed. You may consider restarting this machine, in case there is ongoing windows updates or services changes, and restarting this installer. If this happens again, please report us the issue at support@bleemeo.com." 
    Abort "The installation of the Windows service failed !"
  ${EndIf}

  # Start the service
  nsExec::ExecToLog 'sc.exe start "${AGENT_SERVICE_NAME}"'
SectionEnd


Section "Uninstall"
  # Unregister our uninstaller
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\glouton"

  nsExec::ExecToLog 'sc.exe stop "${AGENT_SERVICE_NAME}"'
  nsExec::ExecToLog 'sc.exe delete "${AGENT_SERVICE_NAME}"'

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
