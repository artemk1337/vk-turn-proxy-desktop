param([string]$MakeNSIS = 'makensis')

$ErrorActionPreference = 'Stop'
$directory = Join-Path ([System.IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
New-Item -ItemType Directory $directory | Out-Null
try {
    Copy-Item (Join-Path $PSScriptRoot 'require-idle-executable.nsh') $directory
    $source = @'
Unicode true
!include "MUI2.nsh"
!include "require-idle-executable.nsh"
Name "Executable wait test"
OutFile "idle-test.exe"
RequestExecutionLevel user
!insertmacro MUI_LANGUAGE "English"
LangString CloseClient ${LANG_ENGLISH} "Close client"
!insertmacro DefineRequireIdleExecutable ""
Section
  Push "$EXEDIR\locked.exe"
  Call RequireIdleExecutable
SectionEnd
'@
    $script = Join-Path $directory 'idle-test.nsi'
    Set-Content $script $source -Encoding UTF8
    & $MakeNSIS -WX $script
    if ($LASTEXITCODE -ne 0) { throw 'NSIS compilation failed' }
    $executable = Join-Path $directory 'idle-test.exe'
    $image = Join-Path $directory 'locked.exe'
    foreach ($scenario in @('missing', 'idle', 'closing', 'busy')) {
        $lock = $null
        if ($scenario -ne 'missing') { [System.IO.File]::WriteAllText($image, 'test') }
        if ($scenario -in @('closing', 'busy')) {
            $lock = [System.IO.File]::Open($image, 'Open', 'Read', 'Read')
        }
        $timer = [System.Diagnostics.Stopwatch]::StartNew()
        $process = Start-Process $executable -ArgumentList '/S' -PassThru
        try {
            if ($scenario -eq 'closing') {
                Start-Sleep -Milliseconds 1000
                if ($process.HasExited) { throw 'Installer did not wait for the locked file' }
                $lock.Dispose()
                $lock = $null
            }
            if (-not $process.WaitForExit(15000)) { throw "$scenario timed out" }
            $expected = if ($scenario -eq 'busy') { 1618 } else { 0 }
            if ($process.ExitCode -ne $expected) { throw "$scenario returned $($process.ExitCode), expected $expected" }
            if ($scenario -eq 'busy' -and $timer.ElapsedMilliseconds -lt 5000) {
                throw 'Installer skipped the retry interval'
            }
            Write-Output "$scenario passed"
        } finally {
            if ($null -ne $lock) { $lock.Dispose() }
            if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() }
        }
    }
} finally {
    Remove-Item $directory -Recurse -Force
}
