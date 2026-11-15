Add-Type @'
using System;
using System.Runtime.InteropServices;
using System.Text;
public class TrayMenuAutomation {
    public delegate bool EnumProc(IntPtr window, IntPtr data);
    [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left, Top, Right, Bottom; }
    [StructLayout(LayoutKind.Sequential)] public struct Point { public int X, Y; }
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint process);
    [DllImport("user32.dll")] public static extern bool EnumThreadWindows(uint thread, EnumProc callback, IntPtr data);
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr window, StringBuilder name, int count);
    [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr window, uint message, IntPtr wparam, IntPtr lparam);
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window, uint message, IntPtr wparam, IntPtr lparam);
    [DllImport("user32.dll")] public static extern int GetMenuItemCount(IntPtr menu);
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetMenuString(IntPtr menu, uint item, StringBuilder value, int count, uint flags);
    [DllImport("user32.dll", SetLastError=true)] public static extern bool GetMenuItemRect(IntPtr window, IntPtr menu, uint item, out Rect rect);
    [DllImport("user32.dll", SetLastError=true)] public static extern bool SetCursorPos(int x, int y);
    [DllImport("user32.dll")] public static extern bool GetCursorPos(out Point point);
    [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] public static extern uint GetMenuState(IntPtr menu, uint item, uint flags);
    [StructLayout(LayoutKind.Sequential)] public struct MouseInput {
        public int X, Y;
        public uint Data, Flags, Time;
        public UIntPtr ExtraInfo;
    }
    [StructLayout(LayoutKind.Sequential)] public struct Input {
        public uint Type;
        public MouseInput Mouse;
    }
    [DllImport("user32.dll", SetLastError=true)] public static extern uint SendInput(uint count, Input[] input, int size);
    public static uint ClickLeftButton() {
        var input = new Input[2];
        input[0].Mouse.Flags = 0x0002;
        input[1].Mouse.Flags = 0x0004;
        return SendInput(2, input, Marshal.SizeOf(typeof(Input)));
    }
}
'@

function Invoke-TrayMenuExit([IntPtr]$WindowHandle, [Diagnostics.Process]$Client, [uint32]$TrayMessage = 0x8000, [int]$TrayEvent = 0x7b) {
    $clientProcessId = [uint32]0
    $thread = [TrayMenuAutomation]::GetWindowThreadProcessId($WindowHandle, [ref]$clientProcessId)
    if ($clientProcessId -ne $Client.Id) { throw 'Tray owner does not belong to the client' }
    [TrayMenuAutomation]::PostMessage($WindowHandle, $TrayMessage, [IntPtr]::Zero, [IntPtr]$TrayEvent) | Out-Null
    $script:trayPopup = [IntPtr]::Zero
    $findPopup = [TrayMenuAutomation+EnumProc] {
        param($handle, $data)
        $name = New-Object Text.StringBuilder 64
        [TrayMenuAutomation]::GetClassName($handle, $name, $name.Capacity) | Out-Null
        if ($name.ToString() -eq '#32768') { $script:trayPopup = $handle; return $false }
        return $true
    }
    $deadline = (Get-Date).AddSeconds(5)
    do {
        [TrayMenuAutomation]::EnumThreadWindows($thread, $findPopup, [IntPtr]::Zero) | Out-Null
        if ($script:trayPopup -eq [IntPtr]::Zero) { Start-Sleep -Milliseconds 100 }
    } until ($script:trayPopup -ne [IntPtr]::Zero -or (Get-Date) -gt $deadline)
    if ($script:trayPopup -eq [IntPtr]::Zero) { throw 'The native tray context menu did not open' }
    # MN_GETHMENU is the standard popup-window query used by menu automation.
    $menu = [TrayMenuAutomation]::SendMessage($script:trayPopup, 0x1e1, [IntPtr]::Zero, [IntPtr]::Zero)
    $last = [TrayMenuAutomation]::GetMenuItemCount($menu) - 1
    if ($last -lt 0) { throw 'The native tray menu is empty' }
    $title = New-Object Text.StringBuilder 256
    [TrayMenuAutomation]::GetMenuString($menu, $last, $title, $title.Capacity, 0x400) | Out-Null
    if ($title.ToString() -notin @('Exit','Выход')) { throw "Unexpected final tray action: $title" }
    $bounds = New-Object TrayMenuAutomation+Rect
    if (-not [TrayMenuAutomation]::GetMenuItemRect([IntPtr]::Zero, $menu, $last, [ref]$bounds)) {
        throw "Cannot locate Exit in the native menu: $([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
    }
    $point = New-Object TrayMenuAutomation+Point
    $point.X = [int](($bounds.Left + $bounds.Right) / 2)
    $point.Y = [int](($bounds.Top + $bounds.Bottom) / 2)
    $menuState = [TrayMenuAutomation]::GetMenuState($menu, $last, 0x400)
    Write-Host "Tray Exit native item '$title', rect=($($bounds.Left),$($bounds.Top),$($bounds.Right),$($bounds.Bottom)), state=$menuState, interactive=$([Environment]::UserInteractive), testSession=$([Diagnostics.Process]::GetCurrentProcess().SessionId), clientSession=$($Client.SessionId)"
    if (-not [TrayMenuAutomation]::SetCursorPos($point.X, $point.Y)) {
        throw "Cannot position the cursor on the native tray Exit action: $([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
    }
    $cursor = New-Object TrayMenuAutomation+Point
    if (-not [TrayMenuAutomation]::GetCursorPos([ref]$cursor) -or $cursor.X -ne $point.X -or $cursor.Y -ne $point.Y) {
        throw 'The input desktop did not accept the tray Exit cursor position'
    }
    # SendInput updates the real button state consumed by TrackPopupMenuEx.
    # Posting WM_LBUTTONDOWN/UP alone leaves GetKeyState(VK_LBUTTON) released.
    Start-Sleep -Milliseconds 100
    $inserted = [TrayMenuAutomation]::ClickLeftButton()
    if ($inserted -ne 2) {
        throw "Tray mouse injection accepted $inserted of 2 events: $([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
    }
    if (-not $Client.WaitForExit(15000)) {
        $menuState = [TrayMenuAutomation]::GetMenuState($menu, $last, 0x400)
        $foreground = [TrayMenuAutomation]::GetForegroundWindow()
        [TrayMenuAutomation]::GetCursorPos([ref]$cursor) | Out-Null
        Write-Host "Tray Exit remained open: menuState=$menuState foreground=$foreground popup=$script:trayPopup cursor=($($cursor.X),$($cursor.Y)) interactive=$([Environment]::UserInteractive)"
        if (Get-Command Save-WindowImage -ErrorAction SilentlyContinue) {
            try { Save-WindowImage $script:trayPopup "$PWD/screenshots/tray-exit-failure-$($Client.Id).png" }
            catch { Write-Host "Tray menu capture failed: $_" }
        }
        throw 'Clicking the native tray Exit action did not close the client'
    }
}
