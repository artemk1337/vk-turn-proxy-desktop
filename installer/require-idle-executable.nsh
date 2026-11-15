; SPDX-License-Identifier: MIT
!include "LogicLib.nsh"

; Allow a closing process five seconds to release its executable.
!macro DefineRequireIdleExecutable Prefix
Function ${Prefix}RequireIdleExecutable
  Pop $1
  retryImage:
    StrCpy $2 20
  checkImage:
    IfFileExists "$1" 0 doneImage
    System::Call 'kernel32::CreateFileW(w r1, i 0x40000000, i 1, p 0, i 3, i 0, p 0) p.r0'
    ${If} $0 == -1
      ${If} $2 > 0
        IntOp $2 $2 - 1
        Sleep 250
        Goto checkImage
      ${EndIf}
      MessageBox MB_ICONEXCLAMATION|MB_RETRYCANCEL "$(CloseClient)" /SD IDCANCEL IDRETRY retryImage
      SetErrorLevel 1618
      Abort
    ${EndIf}
    System::Call 'kernel32::CloseHandle(p r0)'
  doneImage:
FunctionEnd
!macroend
