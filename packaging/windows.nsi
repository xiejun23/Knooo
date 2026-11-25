Unicode true
!include "MUI2.nsh"
!include "x64.nsh"
!include "FileFunc.nsh"

!ifndef APP_VERSION
  !define APP_VERSION "dev"
!endif
!define APP_NAME "Knooo"
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\KnoooStudio"
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
  ${If} $UpdatePID <= 0
  ${OrIf} $1 == ""
  ${OrIfNot} ${FileExists} "$1\Knooo-Studio.exe"
    MessageBox MB_ICONSTOP "Knooo installation could not be found. Run Setup manually."
    SetErrorLevel 1
    Abort
  ${EndIf}
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
    MessageBox MB_ICONSTOP "Knooo did not exit. Close it and run Setup again."
    SetErrorLevel 1
    Abort
  ${EndIf}
FunctionEnd

Function SkipUpdateDirectory
  ${If} $UpdatePID != ""
    Abort
  ${EndIf}
FunctionEnd

Section "Install"
  SetShellVarContext current
  SetOutPath "$INSTDIR"
  SetOverwrite try
  ClearErrors
  File /oname=Knooo-Studio.exe "..\build\Knooo-windows-x64.exe"
  ${If} ${Errors}
    MessageBox MB_ICONSTOP "Could not write ${APP_NAME}. Close any running copy and try again." /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}

  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateShortcut "$SMPROGRAMS\${APP_NAME}.lnk" "$INSTDIR\Knooo-Studio.exe"
  Delete "$SMPROGRAMS\Knooo Studio.lnk"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "${APP_NAME}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${APP_VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\Knooo-Studio.exe"
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
    Exec '"$INSTDIR\Knooo-Studio.exe"'
  ${EndIf}
FunctionEnd

Section "Uninstall"
  SetShellVarContext current
  ClearErrors
  Delete "$INSTDIR\Knooo-Studio.exe"
  ${If} ${Errors}
    MessageBox MB_ICONSTOP "Close ${APP_NAME} before uninstalling." /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}

  Delete "$SMPROGRAMS\${APP_NAME}.lnk"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
SectionEnd
