$ErrorActionPreference = 'Stop'
$backendRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$backendExe = Join-Path $backendRoot 'wb2api.exe'
$backendConfig = Join-Path $backendRoot 'config.json'
$backendLog = Join-Path $backendRoot 'data/backend-startup.log'
$backendMutex = $null
$hasLock = $false
try {
    if (!(Test-Path -LiteralPath $backendExe) -or !(Test-Path -LiteralPath $backendConfig)) {
        throw 'Backend executable or config.json is missing.'
    }
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $mutexId = ([BitConverter]::ToString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($backendRoot.ToLowerInvariant())))).Replace('-', '').Substring(0, 20) }
    finally { $sha.Dispose() }
    $backendMutex = New-Object Threading.Mutex($false, ('Local\WorkBuddyAccountsBackend-' + $mutexId))
    try { $hasLock = $backendMutex.WaitOne(0) }
    catch [Threading.AbandonedMutexException] { $hasLock = $true }
    if (!$hasLock) { exit 0 }
    New-Item -ItemType Directory -Path (Join-Path $backendRoot 'data') -Force | Out-Null
    $env:WB2A_LISTEN = '127.0.0.1:7863'
    while ($true) {
        # Adopt an already running process from this checkout instead of starting
        # another instance. Keep supervising it after a manual deployment, too.
        $existingBackend = Get-CimInstance Win32_Process -Filter "Name='wb2api.exe'" |
            Where-Object { $_.ExecutablePath -eq $backendExe } | Select-Object -First 1
        if ($existingBackend) {
            Add-Content -LiteralPath $backendLog -Value ((Get-Date -Format 's') + ' Supervising existing backend PID ' + $existingBackend.ProcessId + '.')
            Wait-Process -Id $existingBackend.ProcessId -ErrorAction SilentlyContinue
        } else {
            foreach ($stream in @('stdout', 'stderr')) {
                $streamLog = Join-Path $backendRoot ('data/local-server.' + $stream + '.log')
                if (Test-Path -LiteralPath $streamLog) { Copy-Item -LiteralPath $streamLog -Destination ($streamLog + '.previous') -Force }
            }
            try {
                $backendProcess = Start-Process -FilePath $backendExe -ArgumentList @('-desktop=false', '-gateway', '-silent', '-config', ('"' + $backendConfig + '"')) `
                    -WorkingDirectory $backendRoot -WindowStyle Hidden -PassThru `
                    -RedirectStandardOutput (Join-Path $backendRoot 'data/local-server.stdout.log') `
                    -RedirectStandardError (Join-Path $backendRoot 'data/local-server.stderr.log')
                Add-Content -LiteralPath $backendLog -Value ((Get-Date -Format 's') + ' Started backend PID ' + $backendProcess.Id + ' on 127.0.0.1:7863.')
                $backendHandle = $backendProcess.Handle
                $backendProcess.WaitForExit()
                Add-Content -LiteralPath $backendLog -Value ((Get-Date -Format 's') + ' Backend exited with code ' + $backendProcess.ExitCode + '.')
                $backendProcess.Dispose()
            } catch {
                Add-Content -LiteralPath $backendLog -Value ((Get-Date -Format 's') + ' Launch failed: ' + $_.Exception.Message)
            }
        }
        Add-Content -LiteralPath $backendLog -Value ((Get-Date -Format 's') + ' Restarting backend in 5 seconds.')
        Start-Sleep -Seconds 5
    }
} catch {
    Add-Content -LiteralPath $backendLog -Value ((Get-Date -Format 's') + ' ' + $_.Exception.Message)
    exit 1
} finally {
    if ($hasLock) { $backendMutex.ReleaseMutex() }
    if ($backendMutex) { $backendMutex.Dispose() }
}
