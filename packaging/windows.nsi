Unicode true
!include "MUI2.nsh"
!include "x64.nsh"
!include "FileFunc.nsh"

!ifndef APP_VERSION
  !define APP_VERSION "dev"
!endif
!define APP_NAME "Knooo"
!define APP_EXE "Knooo.exe"
!define LEGACY_EXE "Knooo-Studio.exe"
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\KnoooStudio"
!define RUN_KEY "Software\Microsoft\Windows\CurrentVersion\Run"
!define WEBVIEW2_KEY "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
!define WEBVIEW2_BOOTSTRAPPER "https://go.microsoft.com/fwlink/p/?LinkId=2124703"
!define MUI_ICON "eagle.ico"
!define MUI_UNICON "eagle.ico"

Name "${APP_NAME}"
OutFile "..\build\Knooo-Setup-windows-x64.exe"
InstallDir "$LOCALAPPDATA\Programs\${APP_NAME}"
InstallDirRegKey HKCU "${UNINSTALL_KEY}" "InstallLocation"
RequestExecutionLevel user
SetCompressor /SOLID lzma
SetDateSave off

Var UpdatePID
!define MUI_PAGE_CUSTOMFUNCTION_PRE SkipUpdateDirectory
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  ${IfNot} ${IsNativeAMD64}
    MessageBox MB_ICONSTOP "${APP_NAME} requires Windows x64." /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}
  ${GetParameters} $0
  ClearErrors
  ${GetOptions} $0 "/UPDATEPID=" $UpdatePID
  ${If} ${Errors}
    StrCpy $UpdatePID ""
    Return
  ${EndIf}
  ReadRegStr $1 HKCU "${UNINSTALL_KEY}" "InstallLocation"
  ${If} $UpdatePID > 0
  ${AndIf} $1 != ""
    ${If} ${FileExists} "$1\${APP_EXE}"
    ${OrIf} ${FileExists} "$1\${LEGACY_EXE}"
      Goto wait_for_update
    ${EndIf}
  ${EndIf}
  MessageBox MB_ICONSTOP "Knooo installation could not be found. Run Setup manually." /SD IDOK
  SetErrorLevel 1
  Abort

wait_for_update:
  StrCpy $INSTDIR "$1"

  System::Call 'kernel32::OpenProcess(i 0x00100000, i 0, i $UpdatePID) p .r0 ?e'
  Pop $1
  ${If} $0 P<> 0
    System::Call 'kernel32::WaitForSingleObject(p r0, i 45000) i .r1'
    System::Call 'kernel32::CloseHandle(p r0)'
  ${ElseIf} $1 = 87
    StrCpy $1 0
  ${EndIf}
  ${If} $1 <> 0
    MessageBox MB_ICONSTOP "Knooo did not exit. Close it and run Setup again." /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}
FunctionEnd

Function SkipUpdateDirectory
  ${If} $UpdatePID != ""
    Abort
  ${EndIf}
FunctionEnd

Function WebView2Version
  ReadRegStr $0 HKLM "${WEBVIEW2_KEY}" "pv"
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    ReadRegStr $0 HKCU "${WEBVIEW2_KEY}" "pv"
  ${EndIf}
FunctionEnd

Function EnsureWebView2
  Call WebView2Version
  ${If} $0 != ""
  ${AndIf} $0 != "0.0.0.0"
    Return
  ${EndIf}

  DetailPrint "Installing Microsoft Edge WebView2 Runtime..."
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  System::Call 'urlmon::URLDownloadToFileW(p 0, w "${WEBVIEW2_BOOTSTRAPPER}", w "$PLUGINSDIR\MicrosoftEdgeWebView2Setup.exe", i 0, p 0) i .r0'
  ${If} $0 == 0
    nsExec::Exec '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -Command "$$sig = Get-AuthenticodeSignature -LiteralPath ./MicrosoftEdgeWebView2Setup.exe; if ($$sig.Status -ne $\'Valid$\' -or $$sig.SignerCertificate.Subject -notmatch $\'(^|, )O=Microsoft Corporation(,|$$)$\') { exit 1 }"'
    Pop $0
    ${If} $0 == 0
      ExecWait '"$PLUGINSDIR\MicrosoftEdgeWebView2Setup.exe" /silent /install' $0
      Call WebView2Version
      ${If} $0 != ""
      ${AndIf} $0 != "0.0.0.0"
        Return
      ${EndIf}
    ${EndIf}
  ${EndIf}

  MessageBox MB_ICONSTOP "Install Microsoft Edge WebView2 Runtime from microsoft.com and run Knooo Setup again." /SD IDOK
  SetErrorLevel 1
  Abort
FunctionEnd

Section "Install"
  SetShellVarContext current
  Call EnsureWebView2
  StrCpy $1 ""
  ReadRegStr $0 HKCU "${UNINSTALL_KEY}" "InstallLocation"
  ${If} $0 != ""
  ${AndIf} ${FileExists} "$0\${LEGACY_EXE}"
    GetFullPathName $1 "$0\${LEGACY_EXE}"
    GetFullPathName $0 "$INSTDIR\${LEGACY_EXE}"
    ${If} $1 != $0
      MessageBox MB_ICONSTOP "To upgrade Knooo, select its current install folder. To move it, uninstall Knooo first." /SD IDOK
      SetErrorLevel 1
      Abort
    ${EndIf}
  ${EndIf}

  StrCpy $2 ""
  ${If} ${FileExists} "$INSTDIR\${APP_EXE}"
    StrCpy $2 "present"
  ${EndIf}
  SetOutPath "$INSTDIR"
  SetOverwrite try
  ClearErrors
  File /oname=${APP_EXE} "..\build\Knooo-windows-x64.exe"
  ${If} ${Errors}
    MessageBox MB_ICONSTOP "Could not write ${APP_NAME}. Close any running copy and try again." /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}

  ${If} $1 != ""
    ReadRegStr $0 HKCU "${RUN_KEY}" "KnoooStudio"
    ${If} $0 == '"$1" --tray'
      ClearErrors
      WriteRegStr HKCU "${RUN_KEY}" "KnoooStudio" '"$INSTDIR\${APP_EXE}" --tray'
      ${If} ${Errors}
        ${If} $2 == ""
          Delete "$INSTDIR\${APP_EXE}"
        ${EndIf}
        MessageBox MB_ICONSTOP "Could not update the Knooo login startup entry." /SD IDOK
        SetErrorLevel 1
        Abort
      ${EndIf}
    ${EndIf}

    ClearErrors
    Delete "$1"
    ${If} ${Errors}
      ${If} $0 == '"$1" --tray'
        WriteRegStr HKCU "${RUN_KEY}" "KnoooStudio" "$0"
      ${EndIf}
      ${If} $2 == ""
        Delete "$INSTDIR\${APP_EXE}"
      ${EndIf}
      MessageBox MB_ICONSTOP "Quit ${APP_NAME} from the system tray before upgrading." /SD IDOK
      SetErrorLevel 1
      Abort
    ${EndIf}
  ${EndIf}

  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateShortcut "$SMPROGRAMS\${APP_NAME}.lnk" "$INSTDIR\${APP_EXE}"
  Delete "$SMPROGRAMS\Knooo Studio.lnk"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "${APP_NAME}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${APP_VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\${APP_EXE}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
  ${If} $UpdatePID != ""
    SetAutoClose true
  ${EndIf}
SectionEnd

Function .onInstSuccess
  ${If} $UpdatePID != ""
    Exec '"$INSTDIR\${APP_EXE}"'
  ${EndIf}
FunctionEnd

Section "Uninstall"
  SetShellVarContext current
  ClearErrors
  Delete "$INSTDIR\${APP_EXE}"
  ${If} ${Errors}
    MessageBox MB_ICONSTOP "Quit ${APP_NAME} from the system tray before uninstalling." /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}

  Delete "$SMPROGRAMS\${APP_NAME}.lnk"
  DeleteRegValue HKCU "${RUN_KEY}" "KnoooStudio"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
SectionEnd
