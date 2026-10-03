; Antares for Windows: a per-user installer (no administrator prompt).
;
; Installs AntaresDesktop.exe (the desktop shell) and antares.exe (the server
; it starts for local connections; Windows names are case-insensitive, so the
; shell cannot also be called Antares.exe) into %LOCALAPPDATA%\Programs\Antares, adds
; Start menu and desktop shortcuts, and registers an uninstaller under
; Apps & features.
;
;   makensis /DVERSION=0.6.0 /DARCH=x64 /DSRC=<dir with both exes> /DOUT=<setup.exe> installer.nsi

Unicode true
ManifestDPIAware true
SetCompressor /SOLID lzma
RequestExecutionLevel user

!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef ARCH
  !define ARCH "x64"
!endif
!ifndef SRC
  !error "pass /DSRC=<directory with AntaresDesktop.exe and antares.exe>"
!endif
!ifndef OUT
  !define OUT "Antares-windows-${ARCH}-setup.exe"
!endif

!define APPNAME "Antares"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Antares"

!include "MUI2.nsh"

Name "${APPNAME}"
OutFile "${OUT}"
InstallDir "$LOCALAPPDATA\Programs\Antares"
InstallDirRegKey HKCU "${UNINSTKEY}" "InstallLocation"
BrandingText "Antares ${VERSION}"

VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "Antares"
VIAddVersionKey "FileDescription" "Antares installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "enowdev, Apache-2.0"

!define MUI_ICON "icon.ico"
!define MUI_UNICON "icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\AntaresDesktop.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open Antares"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Section "Install"
  SetOutPath "$INSTDIR"
  ; A running app or server holds its exe open; stop both before overwriting.
  nsExec::Exec 'taskkill /IM AntaresDesktop.exe /F'
  IfFileExists "$INSTDIR\antares.exe" 0 +2
    nsExec::Exec '"$INSTDIR\antares.exe" stop'
  File "${SRC}\AntaresDesktop.exe"
  File "${SRC}\antares.exe"
  File "icon.ico"
  WriteUninstaller "$INSTDIR\Uninstall.exe"

  CreateShortcut "$SMPROGRAMS\Antares.lnk" "$INSTDIR\AntaresDesktop.exe" "" "$INSTDIR\icon.ico"
  CreateShortcut "$DESKTOP\Antares.lnk" "$INSTDIR\AntaresDesktop.exe" "" "$INSTDIR\icon.ico"

  WriteRegStr HKCU "${UNINSTKEY}" "DisplayName" "Antares"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTKEY}" "Publisher" "enowdev"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\icon.ico"
  WriteRegStr HKCU "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTKEY}" "URLInfoAbout" "https://antares.enowx.ai"
  WriteRegStr HKCU "${UNINSTKEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoRepair" 1
SectionEnd

Section "Uninstall"
  nsExec::Exec 'taskkill /IM AntaresDesktop.exe /F'
  nsExec::Exec '"$INSTDIR\antares.exe" stop'
  Delete "$SMPROGRAMS\Antares.lnk"
  Delete "$DESKTOP\Antares.lnk"
  Delete "$INSTDIR\AntaresDesktop.exe"
  Delete "$INSTDIR\antares.exe"
  Delete "$INSTDIR\icon.ico"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "${UNINSTKEY}"
  ; Your data in %USERPROFILE%\.antares is left alone.
SectionEnd
