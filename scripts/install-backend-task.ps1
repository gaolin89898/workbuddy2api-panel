$ErrorActionPreference = 'Stop'
$backendRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$backendRunner = Join-Path $PSScriptRoot 'run-backend.ps1'
$taskName = 'WorkBuddyAccountsBackend'
$backendUser = [Security.Principal.WindowsIdentity]::GetCurrent().Name
if (!(Test-Path -LiteralPath (Join-Path $backendRoot 'wb2api.exe')) -or !(Test-Path -LiteralPath (Join-Path $backendRoot 'config.json'))) {
    throw 'Build wb2api.exe and prepare config.json before installing the backend task.'
}
$previousTask = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
if ($previousTask) {
    New-Item -ItemType Directory -Path (Join-Path $backendRoot 'data/startup') -Force | Out-Null
    Export-ScheduledTask -TaskName $taskName | Set-Content -LiteralPath (Join-Path $backendRoot 'data/startup/backend-task.previous.xml') -Encoding Unicode
    Stop-ScheduledTask -TaskName $taskName
}
# Older tasks ran through wscript.exe; stopping that task leaves its PowerShell
# child alive with the supervisor mutex. Retire that wrapper, retaining wb2api.
$legacyRunner = Join-Path $backendRoot 'data/startup/start-backend.ps1'
Get-CimInstance Win32_Process -Filter "Name='powershell.exe'" | Where-Object {
    $_.CommandLine -and $_.CommandLine.Contains('"' + $legacyRunner + '"')
} | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
# Windows Terminal 设为默认终端后，任务直接拉起 powershell.exe 仍会被 WT 接管
# 成可见窗口（-WindowStyle Hidden 失效）。conhost --headless 显式指定无头控制台
# 宿主，绕过 WT 委派，任务与后端全程无窗口。
$backendAction = New-ScheduledTaskAction -Execute (Join-Path $env:SystemRoot 'System32/conhost.exe') `
    -Argument ('--headless ' + (Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe') + ' -NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -File "' + $backendRunner + '"') -WorkingDirectory $backendRoot
$backendTriggers = @(
    (New-ScheduledTaskTrigger -AtLogOn -User $backendUser),
    (New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(1) -RepetitionInterval (New-TimeSpan -Minutes 1))
)
$backendSettings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew `
    -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
$backendPrincipal = New-ScheduledTaskPrincipal -UserId $backendUser -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName $taskName -Action $backendAction -Trigger $backendTriggers -Settings $backendSettings `
    -Principal $backendPrincipal -Description 'Local account backend: logon startup, automatic process recovery, loopback only.' -Force | Out-Null
Start-ScheduledTask -TaskName $taskName
Write-Output 'Backend task installed and started. Logs: data/backend-startup.log and data/local-server.stderr.log.'
