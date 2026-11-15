$ErrorActionPreference = 'Stop'
$global:stopClientTest = @{}
$directory = Join-Path ([System.IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
New-Item -ItemType Directory $directory | Out-Null
try {
    function Get-CimInstance {
        param($ClassName, $Filter)
        $global:stopClientTest.queries++
        if ($global:stopClientTest.queries -gt 1) {
            if ($global:stopClientTest.scenario -eq 'new-backend') { return $global:stopClientTest.newBackend }
            return @()
        }
        return $global:stopClientTest.backends
    }
    function Get-Process {
        param($Id, $Name, $ErrorAction)
        if ($Name) { return $global:stopClientTest.clients }
        return $global:stopClientTest.processes[$Id]
    }
    function Stop-Process {
        param($InputObject, [switch]$Force)
        $global:stopClientTest.events.Add("kill:$($InputObject.Id)")
    }
    function New-TestProcess($Id, $Path, $Exits) {
        $process = [pscustomobject]@{ Id = $Id; Path = $Path; Exits = $Exits }
        $process | Add-Member ScriptMethod WaitForExit {
            param($Timeout)
            $global:stopClientTest.events.Add("wait:$($this.Id)")
            return $this.Exits
        }
        return $process
    }
    foreach ($scenario in @('idle', 'connected', 'timeout', 'other-copy', 'new-backend')) {
        $global:stopClientTest.scenario = $scenario
        $global:stopClientTest.queries = 0
        $global:stopClientTest.events = [System.Collections.Generic.List[string]]::new()
        $gui = New-TestProcess 10 (Join-Path $directory 'vk-turn-proxy-windows.exe') $true
        $console = New-TestProcess 20 (Join-Path $directory 'vk-turn-proxy-console.exe') ($scenario -ne 'timeout')
        $stopFile = Join-Path $directory 'stop request'
        Remove-Item $stopFile -ErrorAction SilentlyContinue
        $global:stopClientTest.clients = @($gui)
        $global:stopClientTest.processes = @{ 20 = $console }
        $global:stopClientTest.processes[21] = New-TestProcess 21 $console.Path $true
        $global:stopClientTest.newBackend = [pscustomobject]@{
            ProcessId = 21; ExecutablePath = $console.Path
            CommandLine = "console.exe -stop-file=`"$stopFile`""
        }
        $global:stopClientTest.backends = @()
        if ($scenario -ne 'idle') {
            $global:stopClientTest.backends = @([pscustomobject]@{
                ProcessId = 20; ExecutablePath = $console.Path
                CommandLine = "console.exe -stop-file `"$stopFile`""
            })
        }
        if ($scenario -eq 'other-copy') {
            $gui.Path = Join-Path $directory 'another-gui.exe'
            $global:stopClientTest.backends[0].ExecutablePath = Join-Path $directory 'another-console.exe'
        }
        $failed = $false
        try { & (Join-Path $PSScriptRoot 'stop-client.ps1') -Directory $directory }
        catch {
            if ($scenario -ne 'timeout' -or $_ -notmatch 'VPN shutdown timed out') { throw }
            $failed = $true
        }
        $actual = $global:stopClientTest.events -join ','
        $expected = switch ($scenario) {
            'idle' { 'kill:10,wait:10' }
            'connected' { 'wait:20,kill:10,wait:10' }
            'timeout' { 'wait:20' }
            'other-copy' { '' }
            'new-backend' { 'wait:20,kill:10,wait:10,wait:21' }
        }
        if ($actual -ne $expected) { throw "$scenario order was $actual, expected $expected" }
        if (($scenario -eq 'timeout') -ne $failed) { throw 'Incorrect timeout result' }
        if ($scenario -in @('connected', 'timeout', 'new-backend') -and -not (Test-Path $stopFile)) {
            throw 'Missing graceful stop request'
        }
        Write-Output "$scenario passed"
    }
} finally {
    Remove-Item $directory -Recurse -Force
    Remove-Variable stopClientTest -Scope Global
}
