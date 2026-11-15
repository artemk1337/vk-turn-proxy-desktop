param([Parameter(Mandatory=$true)][string]$Directory)

$ErrorActionPreference = 'Stop'
$guiPath = Join-Path $Directory 'vk-turn-proxy-windows.exe'
$consolePath = Join-Path $Directory 'vk-turn-proxy-console.exe'

function Stop-Console {
    $backends = @(Get-CimInstance Win32_Process -Filter "Name = 'vk-turn-proxy-console.exe'" |
        Where-Object { $_.ExecutablePath -eq $consolePath })
    foreach ($backend in $backends) {
        $process = Get-Process -Id $backend.ProcessId -ErrorAction SilentlyContinue
        if ($null -eq $process -or $process.Path -ne $consolePath) { continue }
        if ($backend.CommandLine -match '(?:^|\s)-stop-file(?:=|\s+)(?:"([^"]+)"|(\S+))') {
            $stopFile = if ($Matches[1]) { $Matches[1] } else { $Matches[2] }
            try { [System.IO.File]::WriteAllBytes($stopFile, [byte[]]@()) }
            catch { if (-not $process.HasExited) { throw } }
        }
        # The backend restores routes and DNS before exiting.
        if (-not $process.WaitForExit(60000)) { throw 'VPN shutdown timed out' }
    }
}

Stop-Console
$clients = @(Get-Process -Name 'vk-turn-proxy-windows' -ErrorAction SilentlyContinue |
    Where-Object { $_.Path -eq $guiPath })
foreach ($client in $clients) {
    try { Stop-Process -InputObject $client -Force }
    catch { if (-not $client.HasExited) { throw } }
    if (-not $client.WaitForExit(10000)) { throw 'Desktop shutdown timed out' }
}
# A GUI may have started another backend while the first one was stopping.
Stop-Console
