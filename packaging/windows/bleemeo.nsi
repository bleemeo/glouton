!include MUI2.nsh
!include LogicLib.nsh
!include FileFunc.nsh
!include x64.nsh

Name "glouton"
OutFile "glouton-installer.exe"
Unicode True

InstallDir "$PROGRAMFILES\glouton"

!define COMPANY_NAME "Bleemeo"
!define PRODUCT_ICON "bleemeo.ico"
!define PRODUCT_NAME "glouton"
!define CONFIGDIR "C:\ProgramData\glouton"
!define PRODUCT_VERSION "0.1"
!define AGENT_SERVICE_NAME "glouton"

!define MUI_ABORTWARNING
!define MUI_ICON ${PRODUCT_ICON}
!define MUI_UNICON ${PRODUCT_ICON}
!define MUI_WELCOMEFINISHPAGE_BITMAP bleemeo_logo.bmp

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
  CreateDirectory "${CONFIGDIR}\conf.d"
  CreateDirectory "${CONFIGDIR}\logs"

  #### CLEANUP ####

  # Delete stray versions of the service
  nsExec::ExecToLog 'sc.exe query "${AGENT_SERVICE_NAME}"'
  Pop $0
  # the service already exists -> let's delete it, and re-install it, in case the config changed
  ${If} $0 == 0
    # Stopping the service is required before try to overwrite its file, if it is already installed
    # Note that 'net.exe' is somewhat synchronous (it timeouts after 20-ish seconds), unlike 'sc.exe'
    # and we *need* to wait for the service to be stopped to be able to overwrite its executable
    nsExec::ExecToLog 'net stop "${AGENT_SERVICE_NAME}"'
    nsExec::ExecToLog 'sc.exe delete "${AGENT_SERVICE_NAME}"'
    # Give the service time to delete itself
    Sleep 1000
  ${EndIf}

  #### UPGRADE FROM BLEEMEO-AGENT ####

  # Delete bleemeo agent if present
  IfFileExists "$PROGRAMFILES\bleemeo-agent\uninstall.exe" 0 old_agent_not_present

  # Move its config & state
  # Beware, if you reinstall the bleemeo agent and you then upgrade glouton, it will overwrite the config files
  # with thoses coming from bleemeo-agent !
  # (We cannot use SetOverwrite as it only impacts the use of File, not CopyFiles :/)
  CopyFiles "C:\ProgramData\bleemeo\etc\agent.conf" "${CONFIGDIR}\glouton.conf"
  CopyFiles "C:\ProgramData\bleemeo\etc\agent.conf.d\*.conf" "${CONFIGDIR}\conf.d\"
  CopyFiles "C:\ProgramData\bleemeo\state.json" "${CONFIGDIR}"

  # Uninstall the bleemeo-agent
  ExecWait '"$PROGRAMFILES\bleemeo-agent\uninstall.exe" /S'

old_agent_not_present:

  #### INSTALLATION ####

  ${If} ${RunningX64}
    File ../../dist/${PRODUCT_NAME}_windows_amd64/${PRODUCT_NAME}.exe
  ${Else}
    File ../../dist/${PRODUCT_NAME}_windows_386/${PRODUCT_NAME}.exe
  ${EndIf}

  # We need the icon to be shown in 'Apps & Features'
  File ${PRODUCT_ICON}
  File windows_update_checker.ps1

  WriteUninstaller "$INSTDIR\Uninstall.exe"

  File "/oname=${CONFIGDIR}\conf.d\05-system.conf" ../../packaging/windows/glouton.conf

  # generate the config file with the account ID and registration ID
  nsExec::ExecToLog '"$INSTDIR\${PRODUCT_NAME}.exe" --post-install --account-id "$AccountIDValue" --registration-key "$RegistrationKeyValue" --basedir "${CONFIGDIR}" --config-file-subpath "conf.d\30-install.conf"'

  # Let's expose proper values in the "Apps & Features" Windows settings by registering our installer
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}" \
                   "DisplayName" "${PRODUCT_NAME} by ${COMPANY_NAME} -- Easy Monitoring for Scalable Infrastructure"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}" \
                   "UninstallString" "$\"$INSTDIR\Uninstall.exe$\""
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}" \
                   "DisplayVersion" "${PRODUCT_VERSION}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}" \
                   "RegOwner" "${COMPANY_NAME}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}" \
                   "RegCompany" "${COMPANY_NAME}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}" \
                   "Publisher" "${COMPANY_NAME}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}" \
                   "InstallLocation" "$\"$INSTDIR$\""
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}" \
                   "DisplayIcon" "$\"$INSTDIR\${PRODUCT_ICON}$\""
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}" \
                   "NoModify" 1
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}" \
                   "NoRepair" 1
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}" \
                   "URLInfoAbout" "https://bleemeo.com/"

  # Compute the folder size to tell windows about the approximate size of glouton
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}" \
                   "EstimatedSize" "$0"

  # Create the service
  nsExec::ExecToLog 'sc.exe create "${AGENT_SERVICE_NAME}" binPath="$INSTDIR\glouton.exe" obj="NT AUTHORITY\LocalService" type=own start=auto DisplayName="${PRODUCT_NAME} by ${COMPANY_NAME} -- Monitoring Agent"'
  # Restart automatically in case of failure
  nsExec::ExecToLog 'sc.exe failure "${AGENT_SERVICE_NAME}" actions=restart/1000 reset=180'

  Pop $0
  ${If} $0 != 0
    MessageBox MB_OK "Service installation failed. You may consider restarting this machine, in case there is ongoing windows updates or services changes, and then restarting this installer. If this happens again, please report us the issue at support@bleemeo.com."
    Abort "The installation of the Windows service failed !"
  ${EndIf}

  # Register the windows update task (first try to delete it in cas it already exists, then add it and run it)
  nsExec::ExecToLog 'schtasks.exe /DELETE /F /TN "${COMPANY_NAME}\${PRODUCT_NAME}\Windows Update Checker"'
  nsExec::ExecToLog 'schtasks.exe /CREATE /RU System /SC HOURLY /TN "${COMPANY_NAME}\${PRODUCT_NAME}\Windows Update Checker" /TR "powershell.exe -NonInteractive -File \"$INSTDIR\windows_update_checker.ps1\""'
  nsExec::ExecToLog 'schtasks.exe /RUN /I /TN "${COMPANY_NAME}\${PRODUCT_NAME}\Windows Update Checker"'

  # Start the service
  nsExec::ExecToLog 'sc.exe start "${AGENT_SERVICE_NAME}"'
SectionEnd


Section "Uninstall"
  # Unregister our uninstaller
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}"

  nsExec::ExecToLog 'net stop "${AGENT_SERVICE_NAME}"'
  nsExec::ExecToLog 'sc.exe delete "${AGENT_SERVICE_NAME}"'

  nsExec::ExecToLog 'schtasks.exe /DELETE /F /TN "${COMPANY_NAME}\${PRODUCT_NAME}\Windows Update Checker"'

  RMDir /r "$INSTDIR"
  # delete the bleemeo folder too if it is empty
  RMDir "$PROGRAMFILES\bleemeo"
SectionEnd


# Create the content of our custom page.
Function informationPage

    # Only show this page if it's the first installation. After first installation, the file
    # 30-install.conf will be created.
    IfFileExists "${CONFIGDIR}\conf.d\30-install.conf" NotFirstInstall 0
    IfFileExists "C:\ProgramData\bleemeo\etc\agent.conf.d\30-install.conf" NotFirstInstall FirstInstall
    NotFirstInstall:
    Abort
    FirstInstall:

    !insertmacro MUI_HEADER_TEXT "Configure your agent" "Enter your credentials to communicate with Bleemeo"

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
