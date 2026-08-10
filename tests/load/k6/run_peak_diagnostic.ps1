param(
  [int]$Vus = 500,
  [string]$Duration = "20s",
  [string]$BaseUrl = "http://127.0.0.1:18380/api/v1",
  [string]$MetricsUrl = "http://127.0.0.1:18380/metrics",
  [string]$RabbitContainer = "gofun-diag-rabbitmq-1",
  [string]$MysqlContainer = "gofun-diag-mysql-1",
  [string]$OutputDir = "tests/load/results/k6-diagnostic-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [switch]$K6InDocker,
  [string]$K6Network = "",
  [string]$K6Image = "grafana/k6:latest"
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$outputPath = Join-Path $repo $OutputDir
New-Item -ItemType Directory -Force -Path $outputPath | Out-Null
$summaryPath = Join-Path $outputPath "k6-summary.json"
$k6StdoutPath = Join-Path $outputPath "k6.stdout.log"
$k6StderrPath = Join-Path $outputPath "k6.stderr.log"
$samples = [System.Collections.Generic.List[object]]::new()

function Get-Maximum([object[]]$values) {
  $numbers = @($values | Where-Object { $_ -ne $null } | ForEach-Object { [double]$_ })
  if ($numbers.Count -eq 0) { return 0 }
  return ($numbers | Measure-Object -Maximum).Maximum
}

function Get-Delta([object[]]$values) {
  $numbers = @($values | Where-Object { $_ -ne $null } | ForEach-Object { [double]$_ })
  if ($numbers.Count -lt 2) { return 0 }
  return $numbers[-1] - $numbers[0]
}

function Get-RabbitSnapshot {
  $rows = @(docker exec $RabbitContainer rabbitmqctl list_queues name messages_ready messages_unacknowledged --quiet 2>$null)
  $ready = 0
  $unacked = 0
  $orderTotal = 0
  $retryTotal = 0
  foreach ($row in $rows) {
    $parts = ($row -split "\s+") | Where-Object { $_ -ne "" }
    if ($parts.Count -ge 3 -and $parts[1] -match '^\d+$' -and $parts[2] -match '^\d+$') {
      $ready += [int64]$parts[1]
      $unacked += [int64]$parts[2]
      $queueTotal = [int64]$parts[1] + [int64]$parts[2]
      if ($parts[0] -eq "fuchang.it.order.queue") { $orderTotal += $queueTotal }
      if ($parts[0] -eq "fuchang.it.order.retry") { $retryTotal += $queueTotal }
    }
  }
  [pscustomobject]@{
    messages_ready = $ready
    messages_unacknowledged = $unacked
    total_messages = $ready + $unacked
    work_queue_total = $orderTotal + $retryTotal
  }
}

function Get-MySqlSnapshot {
  $query = "SHOW GLOBAL STATUS WHERE Variable_name IN ('Innodb_row_lock_current_waits','Innodb_row_lock_waits','Innodb_row_lock_time','Threads_running','Threads_connected');"
  $rows = @(docker exec -e MYSQL_PWD=fuchang-it-mysql $MysqlContainer mysql -ufuchang -N -e $query 2>$null)
  $result = [ordered]@{}
  foreach ($row in $rows) {
    $parts = ($row -split "\s+") | Where-Object { $_ -ne "" }
    if ($parts.Count -ge 2) { $result[$parts[0].ToLowerInvariant()] = [int64]$parts[1] }
  }
  [pscustomobject]$result
}

function Enable-MySqlStatementHistory {
  $query = @"
UPDATE performance_schema.setup_consumers
SET ENABLED = 'YES'
WHERE NAME IN ('events_statements_current', 'events_statements_history', 'events_statements_history_long');
"@
  docker exec -e MYSQL_PWD=fuchang-it-root $MysqlContainer mysql -uroot -N -B -e $query 2>$null | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "failed to enable MySQL statement history" }
}

function Get-MySqlLockWaitSnapshot {
  $query = @"
SELECT
  COALESCE(CAST(r.THREAD_ID AS CHAR), ''),
  COALESCE(CAST(r.EVENT_ID AS CHAR), ''),
  COALESCE(r.OBJECT_SCHEMA, ''),
  COALESCE(r.OBJECT_NAME, ''),
  COALESCE(r.INDEX_NAME, ''),
  COALESCE(r.LOCK_TYPE, ''),
  COALESCE(r.LOCK_MODE, ''),
  COALESCE(r.LOCK_DATA, ''),
  COALESCE(CAST(b.THREAD_ID AS CHAR), ''),
  COALESCE(CAST(b.EVENT_ID AS CHAR), ''),
  COALESCE(b.OBJECT_SCHEMA, ''),
  COALESCE(b.OBJECT_NAME, ''),
  COALESCE(b.INDEX_NAME, ''),
  COALESCE(b.LOCK_TYPE, ''),
  COALESCE(b.LOCK_MODE, ''),
  COALESCE(b.LOCK_DATA, ''),
  COALESCE(REPLACE(REPLACE(REPLACE(COALESCE(NULLIF(rs.SQL_TEXT, ''), (SELECT ps.SQL_TEXT FROM performance_schema.prepared_statements_instances ps WHERE ps.OWNER_THREAD_ID = r.THREAD_ID AND ps.SQL_TEXT LIKE CONCAT('%', r.OBJECT_NAME, '%') ORDER BY ps.COUNT_EXECUTE DESC LIMIT 1), NULLIF(rh.SQL_TEXT, ''), rhl.SQL_TEXT, (SELECT s.SQL_TEXT FROM performance_schema.events_statements_history s WHERE s.THREAD_ID = r.THREAD_ID AND s.EVENT_ID <= r.EVENT_ID ORDER BY s.EVENT_ID DESC LIMIT 1), (SELECT s.SQL_TEXT FROM performance_schema.events_statements_history_long s WHERE s.THREAD_ID = r.THREAD_ID AND s.EVENT_ID <= r.EVENT_ID ORDER BY s.EVENT_ID DESC LIMIT 1)), CHAR(9), ' '), CHAR(10), ' '), CHAR(13), ' '), ''),
  COALESCE(REPLACE(REPLACE(REPLACE(COALESCE(NULLIF(bs.SQL_TEXT, ''), (SELECT ps.SQL_TEXT FROM performance_schema.prepared_statements_instances ps WHERE ps.OWNER_THREAD_ID = b.THREAD_ID AND ps.SQL_TEXT LIKE CONCAT('%', b.OBJECT_NAME, '%') ORDER BY ps.COUNT_EXECUTE DESC LIMIT 1), NULLIF(bh.SQL_TEXT, ''), bhl.SQL_TEXT, (SELECT s.SQL_TEXT FROM performance_schema.events_statements_history s WHERE s.THREAD_ID = b.THREAD_ID AND s.EVENT_ID <= b.EVENT_ID ORDER BY s.EVENT_ID DESC LIMIT 1), (SELECT s.SQL_TEXT FROM performance_schema.events_statements_history_long s WHERE s.THREAD_ID = b.THREAD_ID AND s.EVENT_ID <= b.EVENT_ID ORDER BY s.EVENT_ID DESC LIMIT 1)), CHAR(9), ' '), CHAR(10), ' '), CHAR(13), ' '), '')
FROM performance_schema.data_lock_waits w
JOIN performance_schema.data_locks r
  ON r.ENGINE = w.ENGINE
 AND r.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
JOIN performance_schema.data_locks b
  ON b.ENGINE = w.ENGINE
 AND b.ENGINE_LOCK_ID = w.BLOCKING_ENGINE_LOCK_ID
LEFT JOIN performance_schema.events_statements_current rs
  ON rs.THREAD_ID = r.THREAD_ID AND rs.EVENT_ID = r.EVENT_ID
LEFT JOIN performance_schema.events_statements_current bs
  ON bs.THREAD_ID = b.THREAD_ID AND bs.EVENT_ID = b.EVENT_ID
LEFT JOIN performance_schema.events_statements_history rh
  ON rh.THREAD_ID = r.THREAD_ID AND rh.EVENT_ID = r.EVENT_ID
LEFT JOIN performance_schema.events_statements_history bh
  ON bh.THREAD_ID = b.THREAD_ID AND bh.EVENT_ID = b.EVENT_ID
LEFT JOIN performance_schema.events_statements_history_long rhl
  ON rhl.THREAD_ID = r.THREAD_ID AND rhl.EVENT_ID = r.EVENT_ID
LEFT JOIN performance_schema.events_statements_history_long bhl
  ON bhl.THREAD_ID = b.THREAD_ID AND bhl.EVENT_ID = b.EVENT_ID
WHERE w.ENGINE = 'INNODB';
"@
  $rows = @(docker exec -e MYSQL_PWD=fuchang-it-root $MysqlContainer mysql -uroot -N -B fuchang_ticketing_it -e $query 2>$null)
  $result = @()
  foreach ($row in $rows) {
    $parts = $row -split "`t", 18
    if ($parts.Count -lt 18) { continue }
    $result += [pscustomobject]@{
      requesting_thread_id = $parts[0]
      requesting_event_id = $parts[1]
      requesting_schema = $parts[2]
      requesting_table = $parts[3]
      requesting_index = $parts[4]
      requesting_lock_type = $parts[5]
      requesting_lock_mode = $parts[6]
      requesting_lock_data = $parts[7]
      blocking_thread_id = $parts[8]
      blocking_event_id = $parts[9]
      blocking_schema = $parts[10]
      blocking_table = $parts[11]
      blocking_index = $parts[12]
      blocking_lock_type = $parts[13]
      blocking_lock_mode = $parts[14]
      blocking_lock_data = $parts[15]
      requesting_sql = $parts[16]
      blocking_sql = $parts[17]
    }
  }
  return $result
}

function Get-PrometheusSnapshot {
  try {
    $text = (Invoke-WebRequest -UseBasicParsing -Uri $MetricsUrl -TimeoutSec 3).Content
    $result = [ordered]@{}
    $matches = [regex]::Matches($text, '(?m)^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{([^}]*)\})?\s+([0-9.eE+-]+)$')
    foreach ($match in $matches) {
      $name = $match.Groups[1].Value
      $labels = $match.Groups[2].Value
      $key = if ([string]::IsNullOrWhiteSpace($labels)) { $name } else { "$name{$labels}" }
      $result[$key] = [double]$match.Groups[3].Value
    }
    return [pscustomobject]$result
  } catch {
    return [pscustomobject]@{}
  }
}

$effectiveBaseUrl = $BaseUrl
Enable-MySqlStatementHistory
$samplingStarted = Get-Date
$samples.Add([pscustomobject]@{
  timestamp = $samplingStarted.ToString("o")
  phase = "pre_http"
  elapsed_seconds = 0
  rabbitmq = Get-RabbitSnapshot
  mysql = Get-MySqlSnapshot
  mysql_lock_waits = @(Get-MySqlLockWaitSnapshot)
  prometheus = Get-PrometheusSnapshot
})
if ($K6InDocker) {
  if ([string]::IsNullOrWhiteSpace($K6Network)) {
    throw "K6Network is required when K6InDocker is enabled"
  }
  $effectiveBaseUrl = "http://backend:8080/api/v1"
  $k6Root = Join-Path $repo "tests\load\k6"
  $k6Args = @(
    "run",
    "--rm",
    "--network", $K6Network,
    "--workdir", "/work",
    "--volume", "${k6Root}:/work",
    "--volume", "${outputPath}:/results",
    "--env", "BASE_URL=$effectiveBaseUrl",
    "--env", "K6_SUMMARY=/results/k6-summary.json",
    "--env", "VUS=$Vus",
    "--env", "DURATION=$Duration",
    $K6Image,
    "run",
    "/work/rush_execute.js"
  )
  $process = Start-Process -FilePath "docker" -ArgumentList $k6Args -WorkingDirectory $repo -NoNewWindow -PassThru `
    -RedirectStandardOutput $k6StdoutPath -RedirectStandardError $k6StderrPath
} else {
  $env:BASE_URL = $effectiveBaseUrl
  $env:K6_SUMMARY = $summaryPath
  $k6Args = @(
    "run",
    "-e", "VUS=$Vus",
    "-e", "DURATION=$Duration",
    "-e", "K6_SUMMARY=$summaryPath",
    "tests/load/k6/rush_execute.js"
  )
  $process = Start-Process -FilePath "k6" -ArgumentList $k6Args -WorkingDirectory $repo -NoNewWindow -PassThru
}
while (-not $process.HasExited) {
  $samples.Add([pscustomobject]@{
    timestamp = (Get-Date).ToString("o")
    phase = "http"
    elapsed_seconds = [Math]::Round(((Get-Date) - $samplingStarted).TotalSeconds, 3)
    rabbitmq = Get-RabbitSnapshot
    mysql = Get-MySqlSnapshot
    mysql_lock_waits = @(Get-MySqlLockWaitSnapshot)
    prometheus = Get-PrometheusSnapshot
  })
  Start-Sleep -Seconds 1
  $process.Refresh()
}

$process.Refresh()
$samples | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $outputPath "samples.json")
$lockWaitRows = @($samples | ForEach-Object { @($_.mysql_lock_waits) })
$lockWaitRows | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $outputPath "mysql-lock-waits.json")
$summary = if (Test-Path $summaryPath) {
  Get-Content $summaryPath -Raw -Encoding utf8 | ConvertFrom-Json
} else {
  $null
}

$rabbitTotals = @($samples | ForEach-Object { $_.rabbitmq.total_messages })
$rabbitReady = @($samples | ForEach-Object { $_.rabbitmq.messages_ready })
$rabbitUnacked = @($samples | ForEach-Object { $_.rabbitmq.messages_unacknowledged })
$lockCurrent = @($samples | ForEach-Object { $_.mysql.innodb_row_lock_current_waits })
$lockWaits = @($samples | ForEach-Object { $_.mysql.innodb_row_lock_waits })
$lockTargets = @($lockWaitRows | ForEach-Object {
  "request=$($_.requesting_table)[$($_.requesting_index)] <- block=$($_.blocking_table)[$($_.blocking_index)]"
} | Sort-Object -Unique)

$diagnostic = [pscustomobject]@{
  vus = $Vus
  duration = $Duration
  base_url = $effectiveBaseUrl
  k6_transport = if ($K6InDocker) { "docker-network" } else { "host-port" }
  k6_network = if ($K6InDocker) { $K6Network } else { $null }
  exit_code = $process.ExitCode
  summary = $summary
  peaks = [pscustomobject]@{
    rabbitmq_total_messages = Get-Maximum $rabbitTotals
    rabbitmq_work_queue_total = Get-Maximum @($samples | ForEach-Object { $_.rabbitmq.work_queue_total })
    rabbitmq_messages_ready = Get-Maximum $rabbitReady
    rabbitmq_messages_unacknowledged = Get-Maximum $rabbitUnacked
    innodb_row_lock_current_waits = Get-Maximum $lockCurrent
    innodb_row_lock_waits_delta = Get-Delta $lockWaits
    mysql_lock_wait_samples = $lockWaitRows.Count
    mysql_lock_wait_targets = $lockTargets
  }
  sample_count = $samples.Count
}
$diagnostic | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $outputPath "diagnostic-summary.json")
$diagnostic | ConvertTo-Json -Depth 10
