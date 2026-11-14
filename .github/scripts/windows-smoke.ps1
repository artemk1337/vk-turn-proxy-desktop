param([string]$Executable = (Join-Path $PWD 'vk-turn-proxy-windows.exe'))

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
. (Join-Path $PSScriptRoot 'tray-smoke.ps1')
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class WindowCapture {
    [StructLayout(LayoutKind.Sequential)]
    public struct Rect { public int Left, Top, Right, Bottom; }
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hwnd, out Rect rect);
    [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr hwnd, IntPtr dc, uint flags);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hwnd);
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hwnd, uint message, IntPtr wparam, IntPtr lparam);
    public delegate bool EnumProc(IntPtr hwnd, IntPtr data);
    [DllImport("user32.dll")] public static extern bool EnumChildWindows(IntPtr hwnd, EnumProc callback, IntPtr data);
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr hwnd, System.Text.StringBuilder name, int count);
    [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr hwnd, uint message, IntPtr wparam, IntPtr lparam);
}
'@

function Save-WindowImage($Handle, $Path) {
    $rect = New-Object WindowCapture+Rect
    if (-not [WindowCapture]::GetWindowRect($Handle, [ref]$rect)) { throw 'Cannot read window bounds' }
    $bitmap = New-Object System.Drawing.Bitmap ($rect.Right - $rect.Left), ($rect.Bottom - $rect.Top)
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $dc = $graphics.GetHdc()
    try {
        if (-not [WindowCapture]::PrintWindow($Handle, $dc, 2)) { throw 'Cannot capture desktop window' }
    } finally {
        $graphics.ReleaseHdc($dc)
        $graphics.Dispose()
    }
    try { $bitmap.Save($Path, [System.Drawing.Imaging.ImageFormat]::Png) }
    finally { $bitmap.Dispose() }
}

$preferencesDirectory = Join-Path $env:LOCALAPPDATA 'VKTurnProxyWindows'
$preferencesFile = Join-Path $preferencesDirectory 'preferences.json'
New-Item -ItemType Directory -Force $preferencesDirectory | Out-Null
$originalPreferences = if (Test-Path $preferencesFile) { [System.IO.File]::ReadAllBytes($preferencesFile) } else { $null }
try {
    foreach ($variant in @(@{ language='en'; theme='light'; autoStart=$false }, @{ language='ru'; theme='dark'; autoStart=$false })) {
        $variantName = "$($variant.language)-$($variant.theme)"
        [System.IO.File]::WriteAllText($preferencesFile, ($variant | ConvertTo-Json -Compress))
        $client = Start-Process $Executable -PassThru
        try {
            $deadline = (Get-Date).AddSeconds(30)
            do {
                Start-Sleep -Milliseconds 250
                $client.Refresh()
                if ($client.HasExited) { throw 'Desktop client exited before opening a window' }
            } until ($client.MainWindowHandle -ne 0 -or (Get-Date) -gt $deadline)
            if ($client.MainWindowHandle -eq 0) { throw 'Desktop window did not open' }
            New-Item -ItemType Directory -Force screenshots | Out-Null
            Start-Sleep -Milliseconds 500
            Save-WindowImage $client.MainWindowHandle "$PWD/screenshots/$variantName-profile.png"
            $script:tabHandle = [IntPtr]::Zero
            $callback = [WindowCapture+EnumProc] {
                param($handle, $data)
                $name = New-Object System.Text.StringBuilder 256
                [WindowCapture]::GetClassName($handle, $name, $name.Capacity) | Out-Null
                if ($name.ToString() -eq 'SysTabControl32') { $script:tabHandle = $handle }
                return $true
            }
            [WindowCapture]::EnumChildWindows($client.MainWindowHandle, $callback, [IntPtr]::Zero) | Out-Null
            if ($script:tabHandle -eq [IntPtr]::Zero) { throw 'Setup tabs were not found' }
            $count = [WindowCapture]::SendMessage($script:tabHandle, 0x1304, [IntPtr]::Zero, [IntPtr]::Zero).ToInt32()
            if ($count -ne 3) { throw 'Expected Connection, Log and Settings tabs' }
            for ($i = 0; $i -lt $count; $i++) {
                [WindowCapture]::SendMessage($script:tabHandle, 0x1330, [IntPtr]$i, [IntPtr]::Zero) | Out-Null
                Start-Sleep -Milliseconds 250
                Save-WindowImage $client.MainWindowHandle "$PWD/screenshots/$variantName-tab-$i.png"
            }
            $windowHandle = $client.MainWindowHandle
            if (-not $client.CloseMainWindow()) { throw 'Desktop window did not accept close' }
            Start-Sleep -Milliseconds 300
            $client.Refresh()
            if ($client.HasExited -or [WindowCapture]::IsWindowVisible($windowHandle)) { throw 'Closing the window did not hide it in the tray' }
            [WindowCapture]::PostMessage($windowHandle, 0x8000, [IntPtr]::Zero, [IntPtr]0x202) | Out-Null
            $restoreDeadline = (Get-Date).AddSeconds(10)
            do { Start-Sleep -Milliseconds 100 } until ([WindowCapture]::IsWindowVisible($windowHandle) -or (Get-Date) -gt $restoreDeadline)
            if (-not [WindowCapture]::IsWindowVisible($windowHandle)) { throw 'Tray click did not restore the window' }
            Invoke-TrayMenuExit $windowHandle $client
        } finally {
            if (-not $client.HasExited) { $client.Kill() }
        }
    }
} finally {
    if ($null -ne $originalPreferences) { [System.IO.File]::WriteAllBytes($preferencesFile, $originalPreferences) }
    else { Remove-Item $preferencesFile -ErrorAction SilentlyContinue }
}
