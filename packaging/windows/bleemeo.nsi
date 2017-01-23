[% extends "pyapp_msvcrt.nsi" %]

# Add our custom page after the welcome page.
# Also add a confirmation on uninstall.
# This need to be synchronized with pyapp.nsi from pynsist
[% block ui_pages %]
!insertmacro MUI_PAGE_WELCOME
Page custom informationPage informationPageLeave
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
[% endblock ui_pages %]

[% block sections %]
Var AccountIDTextBox
Var RegistrationKeyTextBox
Var AccountIDValue
Var RegistrationKeyValue


# Add pre-remove, pre-install and post-install hooks.
# This should not need synchronization with pynsist.
Section "Uninstall"
nsExec::ExecToLog '[[ python ]] -Es "$INSTDIR\bleemeo-agent.launch.py" "--pre-remove"'
SectionEnd

Section "!${PRODUCT_NAME}"
nsExec::ExecToLog '[[ python ]] -Es "$INSTDIR\bleemeo-agent.launch.py" "--pre-install"'
SectionEnd

[[ super() ]]

Section "!${PRODUCT_NAME}"
nsExec::ExecToLog '[[ python ]] -Es "$INSTDIR\bleemeo-agent.launch.py" "--post-install" "--account" "$AccountIDValue" "--registration" "$RegistrationKeyValue"'
SectionEnd


# Create the content of our custom page. This should not need synchronization with pynsist.
!include LogicLib.nsh
Function informationPage

    # Only show this page if it's the first installation. After first installation, the file
    # 30-install.conf will be created.
    IfFileExists C:\ProgramData\Bleemeo\etc\agent.conf.d\30-install.conf NotFirstInstall FirstInstall
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

    ${NSD_CreateText} 0 13u 100% 12u ""
    Pop $AccountIDTextBox

    ${NSD_CreateLabel} 0 27u 100% 12u "Enter your registration key"
    Pop $0

    ${NSD_CreateText} 0 40u 100% 12u ""
    Pop $RegistrationKeyTextBox

    nsDialogs::Show
FunctionEnd

Function informationPageLeave
    ${NSD_GetText} $AccountIDTextBox $AccountIDValue
    ${NSD_GetText} $RegistrationKeyTextBox $RegistrationKeyValue
FunctionEnd

[% endblock sections %]


# Do not create shortcuts. This should not need synchronization with pynsist.
[% block install_shortcuts %]
[% endblock install_shortcuts %]
