$ErrorActionPreference = 'Stop'
$installDirectory = Join-Path $env:RUNNER_TEMP 'VK TURN Proxy smoke'
$registryPath = 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\VKTurnProxyWindows'
$taskName = 'VKTurnProxyWindows-' + [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$installer = (Get-ChildItem release-assets -Filter '*amd64-setup.exe').FullName
$armInstaller = (Get-ChildItem release-assets -Filter '*arm64-setup.exe').FullName
$portable = (Get-ChildItem release-assets -Filter '*amd64.zip').FullName
Expand-Archive $portable -DestinationPath portable
$portableDirectory = (Get-ChildItem portable -Directory | Select-Object -First 1).FullName
$arm = Start-Process $armInstaller -ArgumentList '/S' -Wait -PassThru
if ($arm.ExitCode -ne 1633) { throw 'ARM64 installer accepted an x64 machine' }
try {
    $process = Start-Process $installer -ArgumentList "/S /D=$installDirectory" -Wait -PassThru
    if ($process.ExitCode -ne 0) { throw "Installer failed with $($process.ExitCode)" }
    foreach ($file in @('vk-turn-proxy-windows.exe','vk-turn-proxy-console.exe','wintun.dll','README.md','README.ru.md','LICENSE','NOTICE.md','WINTUN-LICENSE.txt','WALK-LICENSE.txt','WAILS-LICENSE.txt','DESKTOP-LICENSES.txt','FONT-LICENSE.txt','WEBVIEW2-LICENSE.txt')) {
        $installed = Join-Path $installDirectory $file
        if (-not (Test-Path $installed)) { throw "Installer omitted $file" }
        if ((Get-FileHash $installed).Hash -ne (Get-FileHash (Join-Path $portableDirectory $file)).Hash) { throw "Installer changed $file" }
    }
    $registration = Get-ItemProperty $registryPath
    if ($registration.DisplayVersion -ne $env:RELEASE_TAG.TrimStart('v')) { throw 'Apps registration has the wrong version' }
    $startMenu = Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'VK TURN Proxy'
    foreach ($shortcut in @('VK TURN Proxy.lnk','Terminal setup.lnk','Uninstall.lnk')) {
        if (-not (Test-Path (Join-Path $startMenu $shortcut))) { throw "Missing Start menu shortcut $shortcut" }
    }
    $console = Join-Path $installDirectory 'vk-turn-proxy-console.exe'
    $version = & $console -version
    if ($LASTEXITCODE -ne 0 -or $version -notmatch [Regex]::Escape($env:RELEASE_TAG)) { throw 'Installed console did not start' }
    $key = [Convert]::ToBase64String([byte[]](1..32))
    $profile = @{version=1; type='connection'; settings=@{serverName='Installer smoke'; privateKey=$key; peerPublicKey=$key; tunnelAddress='10.66.0.2/32'; peerAddress='127.0.0.1:56000'; vkLink='https://vk.ru/call/join/example'; useSrtp=$true}}
    $profilePath = Join-Path $env:RUNNER_TEMP 'installer-profile.json'
    [IO.File]::WriteAllText($profilePath, ($profile | ConvertTo-Json -Depth 4 -Compress))
    $wizard = "1`n$profilePath`n" | & $console -wizard -lang en -list
    if ($LASTEXITCODE -ne 0 -or ($wizard -join "`n") -notmatch 'Installer smoke') { throw 'Installed terminal wizard did not import a profile' }
    & ./.github/scripts/windows-smoke.ps1 -Executable (Join-Path $installDirectory 'vk-turn-proxy-windows.exe')
    if (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) { throw 'An unexpected autostart task already exists' }
    $action = New-ScheduledTaskAction -Execute (Join-Path $installDirectory 'vk-turn-proxy-windows.exe') -Argument '--autostart'
    $principal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value) -LogonType Interactive -RunLevel Highest
    Register-ScheduledTask -TaskName $taskName -Action $action -Principal $principal -Description 'VK TURN Proxy for Windows autostart' | Out-Null
    $uninstaller = Join-Path $installDirectory 'uninstall.exe'
    $removed = Start-Process $uninstaller -ArgumentList '/S' -Wait -PassThru
    if ($removed.ExitCode -ne 0) { throw "Uninstaller failed with $($removed.ExitCode)" }
    if (Test-Path (Join-Path $installDirectory 'vk-turn-proxy-windows.exe')) { throw 'Uninstaller left the GUI executable' }
    if (Test-Path $registryPath) { throw 'Uninstaller left Apps registration' }
    if (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) { throw 'Uninstaller left its autostart task' }
} finally {
    $task = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    if ($task -and $task.Description -eq 'VK TURN Proxy for Windows autostart' -and $task.Actions[0].Execute -eq (Join-Path $installDirectory 'vk-turn-proxy-windows.exe')) {
        Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
    }
}
