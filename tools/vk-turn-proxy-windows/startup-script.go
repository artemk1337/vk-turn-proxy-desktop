// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

const startupTaskPrefix = "VKTurnProxyWindows-"
const startupTaskDescription = "VK TURN Proxy for Windows autostart"

func startupQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// Paths and identities enter PowerShell as quoted data, never executable text.
func startupTaskScript(executable, sid, taskName, operation string) string {
	script := "$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue'; " +
		"[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); $OutputEncoding=[Console]::OutputEncoding; " +
		"$name=" + startupQuote(taskName) + "; $exe=" + startupQuote(executable) + "; " +
		"$sid=" + startupQuote(sid) + "; " +
		"$task=Get-ScheduledTask -TaskPath '\\' -TaskName $name -ErrorAction SilentlyContinue; " +
		"$same=$task -and @($task.Actions).Count -eq 1 -and [String]::Equals($task.Actions[0].Execute,$exe,[StringComparison]::OrdinalIgnoreCase) -and $task.Actions[0].Arguments -eq '--autostart'; "
	switch operation {
	case "query":
		return script + "if ($same -and $task.State -ne 'Disabled') { 'true' } else { 'false' }"
	case "disable":
		return script + "if ($same) { Unregister-ScheduledTask -TaskPath '\\' -TaskName $name -Confirm:$false }"
	case "enable":
		return script + "if ($task -and -not $same -and $task.Description -ne " + startupQuote(startupTaskDescription) + ") { throw 'An unrelated task already uses the autostart task name' }; " +
			"$action=New-ScheduledTaskAction -Execute $exe -Argument '--autostart' -WorkingDirectory " + startupQuote(filepath.Dir(executable)) + "; " +
			"$trigger=New-ScheduledTaskTrigger -AtLogOn -User $sid; " +
			"$principal=New-ScheduledTaskPrincipal -UserId $sid -LogonType Interactive -RunLevel Highest; " +
			"$settings=New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew; " +
			"Register-ScheduledTask -TaskPath '\\' -TaskName $name -Description " + startupQuote(startupTaskDescription) + " -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null"
	default:
		panic(fmt.Sprintf("unsupported startup operation %q", operation))
	}
}
