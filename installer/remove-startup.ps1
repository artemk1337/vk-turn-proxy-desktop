# SPDX-License-Identifier: MIT
param([Parameter(Mandatory = $true)][string]$Executable)
$ErrorActionPreference = 'Stop'
Get-ScheduledTask -TaskName 'VKTurnProxyWindows-*' -ErrorAction SilentlyContinue |
    Where-Object {
        $_.Description -eq 'VK TURN Proxy for Windows autostart' -and
        @($_.Actions).Count -eq 1 -and
        [String]::Equals($_.Actions[0].Execute, $Executable, [StringComparison]::OrdinalIgnoreCase) -and
        $_.Actions[0].Arguments -eq '--autostart'
    } |
    Unregister-ScheduledTask -Confirm:$false
