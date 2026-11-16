; SPDX-License-Identifier: MIT
Unicode true

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"
!include "FileFunc.nsh"

!ifndef VERSION
  !error "Define VERSION, for example 1.0.1"
!endif
!ifndef ARCH
  !error "Define ARCH as amd64 or arm64"
!endif
!ifndef INPUT_DIR
  !error "Define INPUT_DIR with the unpacked release files"
!endif
!ifndef OUTPUT_FILE
  !error "Define OUTPUT_FILE with the installer output path"
!endif

!if "${ARCH}" != "amd64"
!if "${ARCH}" != "arm64"
  !error "ARCH must be amd64 or arm64"
!endif
!endif

!define PRODUCT_NAME "VK TURN Proxy for Windows"
!define MUI_ICON "${INPUT_DIR}/app.ico"
!define MUI_UNICON "${INPUT_DIR}/app.ico"
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\VKTurnProxyWindows"

Name "${PRODUCT_NAME}"
OutFile "${OUTPUT_FILE}"
InstallDir "$PROGRAMFILES64\VK TURN Proxy"
RequestExecutionLevel admin
SetCompressor /SOLID lzma
ShowInstDetails show
ShowUninstDetails show
BrandingText "VK TURN Proxy"
VIProductVersion "${VERSION}.0"
VIAddVersionKey /LANG=1033 "ProductName" "${PRODUCT_NAME}"
VIAddVersionKey /LANG=1033 "FileDescription" "VK TURN Proxy Windows installer (${ARCH})"
VIAddVersionKey /LANG=1033 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "LegalCopyright" "artemk1337 and contributors"

!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\vk-turn-proxy-windows.exe"
!define MUI_FINISHPAGE_RUN_TEXT "$(RunClient)"
!define MUI_LANGDLL_REGISTRY_ROOT "HKLM"
!define MUI_LANGDLL_REGISTRY_KEY "${UNINSTALL_KEY}"
!define MUI_LANGDLL_REGISTRY_VALUENAME "InstallerLanguage"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "../LICENSE"
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_UNPAGE_FINISH
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "Russian"

LangString RunClient ${LANG_ENGLISH} "Open VK TURN Proxy"
LangString RunClient ${LANG_RUSSIAN} "Открыть VK TURN Proxy"
LangString CloseClient ${LANG_ENGLISH} "Close VK TURN Proxy before continuing. Disconnect the VPN first."
LangString CloseClient ${LANG_RUSSIAN} "Перед продолжением отключите VPN и закройте VK TURN Proxy."
LangString ArchitectureError ${LANG_ENGLISH} "This installer is for Windows ${ARCH}. Download the version for your processor."
LangString ArchitectureError ${LANG_RUSSIAN} "Этот установщик предназначен для Windows ${ARCH}. Скачайте версию для своего процессора."
LangString StartupCleanupError ${LANG_ENGLISH} "Windows could not remove the application's autostart task. Close Task Scheduler and try again."
LangString StartupCleanupError ${LANG_RUSSIAN} "Windows не удалось удалить задачу автозапуска приложения. Закройте планировщик заданий и повторите попытку."
LangString InstallFiles ${LANG_ENGLISH} "Application files (required)"
LangString InstallFiles ${LANG_RUSSIAN} "Файлы приложения (обязательно)"
LangString DesktopShortcut ${LANG_ENGLISH} "Desktop shortcut"
LangString DesktopShortcut ${LANG_RUSSIAN} "Ярлык на рабочем столе"

!macro RequireClosed
  checkClient:
    FindWindow $0 "" "VK TURN Proxy"
    ${If} $0 == 0
      FindWindow $0 "" "VK TURN Proxy for Windows"
    ${EndIf}
    ${If} $0 != 0
      MessageBox MB_ICONEXCLAMATION|MB_RETRYCANCEL "$(CloseClient)" /SD IDCANCEL IDRETRY checkClient
      Abort
    ${EndIf}
!macroend

Function .onInit
  SetRegView 64
  ReadRegStr $0 HKLM "${UNINSTALL_KEY}" "InstallLocation"
  ${If} $0 != ""
    StrCpy $INSTDIR $0
  ${EndIf}
  !insertmacro MUI_LANGDLL_DISPLAY
!if "${ARCH}" == "arm64"
  ${IfNot} ${IsNativeARM64}
!else
  ${IfNot} ${IsNativeAMD64}
!endif
    MessageBox MB_ICONSTOP "$(ArchitectureError)" /SD IDOK
    SetErrorLevel 1633
    Abort
  ${EndIf}
FunctionEnd

Function un.onInit
  SetRegView 64
  !insertmacro MUI_UNGETLANGUAGE
  !insertmacro RequireClosed
FunctionEnd

!include "require-idle-executable.nsh"
!insertmacro DefineRequireIdleExecutable ""
!insertmacro DefineRequireIdleExecutable "un."

Section "$(InstallFiles)" SEC_APPLICATION
  SectionIn RO
  SetShellVarContext all
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File "stop-client.ps1"
  stopClient:
    nsExec::ExecToStack '"$WINDIR\Sysnative\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$PLUGINSDIR\stop-client.ps1" -Directory "$INSTDIR"'
    Pop $0
    Pop $1
    ${If} $0 != 0
      MessageBox MB_ICONEXCLAMATION|MB_RETRYCANCEL "$(CloseClient)" /SD IDCANCEL IDRETRY stopClient
      Abort
    ${EndIf}
  Push "$INSTDIR\vk-turn-proxy-windows.exe"
  Call RequireIdleExecutable
  Push "$INSTDIR\vk-turn-proxy-console.exe"
  Call RequireIdleExecutable
  SetOutPath "$INSTDIR"
  SetOverwrite on
  File "${INPUT_DIR}/vk-turn-proxy-windows.exe"
  File "${INPUT_DIR}/vk-turn-proxy-console.exe"
  File "${INPUT_DIR}/wintun.dll"
  File "${INPUT_DIR}/README.md"
  File "${INPUT_DIR}/README.ru.md"
  File "${INPUT_DIR}/README.en.md"
  File "${INPUT_DIR}/LICENSE"
  File "${INPUT_DIR}/NOTICE.md"
  File "${INPUT_DIR}/WINTUN-LICENSE.txt"
  File "${INPUT_DIR}/WALK-LICENSE.txt"
  File "${INPUT_DIR}/WAILS-LICENSE.txt"
  File "${INPUT_DIR}/DESKTOP-LICENSES.txt"
  File "${INPUT_DIR}/FONT-LICENSE.txt"
  File "${INPUT_DIR}/WEBVIEW2-LICENSE.txt"
  File "${INPUT_DIR}/app.ico"
  SetOutPath "$INSTDIR\docs"
  File "${INPUT_DIR}/docs/*.md"
  SetOutPath "$INSTDIR\docs\images"
  File "${INPUT_DIR}/docs/images/main-window-en.png"
  File "${INPUT_DIR}/docs/images/main-window-ru.png"
  SetOutPath "$INSTDIR"
  File "remove-startup.ps1"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateDirectory "$SMPROGRAMS\VK TURN Proxy"
  CreateShortCut "$SMPROGRAMS\VK TURN Proxy\VK TURN Proxy.lnk" "$INSTDIR\vk-turn-proxy-windows.exe"
  CreateShortCut "$SMPROGRAMS\VK TURN Proxy\Terminal setup.lnk" "$INSTDIR\vk-turn-proxy-console.exe" "-wizard"
  CreateShortCut "$SMPROGRAMS\VK TURN Proxy\Uninstall.lnk" "$INSTDIR\uninstall.exe"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayName" "${PRODUCT_NAME}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "Publisher" "artemk1337"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\vk-turn-proxy-windows.exe"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKLM "${UNINSTALL_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKLM "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINSTALL_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  WriteRegDWORD HKLM "${UNINSTALL_KEY}" "EstimatedSize" $0
SectionEnd

Section /o "$(DesktopShortcut)" SEC_DESKTOP
  SetShellVarContext all
  CreateShortCut "$DESKTOP\VK TURN Proxy.lnk" "$INSTDIR\vk-turn-proxy-windows.exe"
SectionEnd


Section "Uninstall"
  SetShellVarContext all
  Push "$INSTDIR\vk-turn-proxy-windows.exe"
  Call un.RequireIdleExecutable
  Push "$INSTDIR\vk-turn-proxy-console.exe"
  Call un.RequireIdleExecutable
  cleanupStartup:
    nsExec::ExecToStack '"$WINDIR\Sysnative\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$INSTDIR\remove-startup.ps1" -Executable "$INSTDIR\vk-turn-proxy-windows.exe"'
    Pop $0
    Pop $1
    ${If} $0 != 0
      MessageBox MB_ICONEXCLAMATION|MB_RETRYCANCEL "$(StartupCleanupError)" /SD IDCANCEL IDRETRY cleanupStartup
      Abort
    ${EndIf}
  ClearErrors
  Delete "$INSTDIR\vk-turn-proxy-windows.exe"
  Delete "$INSTDIR\vk-turn-proxy-console.exe"
  ${If} ${Errors}
    MessageBox MB_ICONEXCLAMATION "$(CloseClient)"
    Abort
  ${EndIf}
  Delete "$INSTDIR\wintun.dll"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\README.ru.md"
  Delete "$INSTDIR\README.en.md"
  Delete "$INSTDIR\docs\guide.ru.md"
  Delete "$INSTDIR\docs\guide.en.md"
  Delete "$INSTDIR\docs\build.ru.md"
  Delete "$INSTDIR\docs\build.en.md"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\NOTICE.md"
  Delete "$INSTDIR\WINTUN-LICENSE.txt"
  Delete "$INSTDIR\WALK-LICENSE.txt"
  Delete "$INSTDIR\WAILS-LICENSE.txt"
  Delete "$INSTDIR\DESKTOP-LICENSES.txt"
  Delete "$INSTDIR\FONT-LICENSE.txt"
  Delete "$INSTDIR\WEBVIEW2-LICENSE.txt"
  Delete "$INSTDIR\app.ico"
  Delete "$INSTDIR\docs\images\main-window-en.png"
  Delete "$INSTDIR\docs\images\main-window-ru.png"
  RMDir "$INSTDIR\docs\images"
  RMDir "$INSTDIR\docs"
  Delete "$INSTDIR\remove-startup.ps1"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$DESKTOP\VK TURN Proxy.lnk"
  Delete "$SMPROGRAMS\VK TURN Proxy\VK TURN Proxy.lnk"
  Delete "$SMPROGRAMS\VK TURN Proxy\Terminal setup.lnk"
  Delete "$SMPROGRAMS\VK TURN Proxy\Uninstall.lnk"
  RMDir "$SMPROGRAMS\VK TURN Proxy"
  DeleteRegKey HKLM "${UNINSTALL_KEY}"
SectionEnd
