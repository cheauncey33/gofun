param(
  [string]$MysqlContainer = "gofun-txprofile-mysql-1",
  [string]$BackendMetricsUrl = "http://127.0.0.1:18580/metrics",
  [string]$OutputDir = "tests/load/results/durability-matrix-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [int]$SingleCommits = 100,
  [int]$ConcurrentWorkers = 6,
  [int]$ConcurrentSeconds = 25,
  [switch]$IncludeConsumerSpot,
  [int]$ConsumerVus = 300,
  [string]$ConsumerDuration = "15s",
  [int]$ConsumerDrainSeconds = 180
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$out = Join-Path $repo $OutputDir
New-Item -ItemType Directory -Force -Path $out | Out-Null

function Mysql([string]$Sql) {
  docker exec -e MYSQL_PWD=fuchang-it-root $MysqlContainer mysql -uroot -N -B -e $Sql 2>$null
}

function Percentile([double[]]$sorted, [double]$p) {
  if ($null -eq $sorted -or $sorted.Count -eq 0) { return $null }
  if ($sorted.Count -eq 1) { return $sorted[0] }
  $rank = ($p / 100.0) * ($sorted.Count - 1)
  $lo = [int][Math]::Floor($rank)
  $hi = [int][Math]::Ceiling($rank)
  if ($lo -eq $hi) { return $sorted[$lo] }
  $w = $rank - $lo
  return $sorted[$lo] * (1 - $w) + $sorted[$hi] * $w
}

function Summarize([double[]]$values) {
  if ($null -eq $values -or $values.Count -eq 0) {
    return [ordered]@{ count = 0; avg_ms = $null; p50_ms = $null; p95_ms = $null; p99_ms = $null }
  }
  $sorted = @($values | Sort-Object)
  [ordered]@{
    count = $sorted.Count
    avg_ms = [Math]::Round(($sorted | Measure-Object -Average).Average, 3)
    p50_ms = [Math]::Round((Percentile $sorted 50), 3)
    p95_ms = [Math]::Round((Percentile $sorted 95), 3)
    p99_ms = [Math]::Round((Percentile $sorted 99), 3)
  }
}

function Parse-Status([string[]]$rows) {
  $map = @{}
  foreach ($r in @($rows)) {
    if (-not $r) { continue }
    $p = @(($r -split "\s+") | Where-Object { $_ -ne "" })
    if ($p.Count -ge 2 -and $p[1] -match '^\d+(\.\d+)?$') { $map[$p[0]] = [double]$p[1] }
  }
  return $map
}

function Set-Durability([int]$FlushLog, [int]$SyncBinlog) {
  Mysql "SET GLOBAL innodb_flush_log_at_trx_commit = $FlushLog; SET GLOBAL sync_binlog = $SyncBinlog;"
  $got = Parse-Status (Mysql "SHOW VARIABLES WHERE Variable_name IN ('innodb_flush_log_at_trx_commit','sync_binlog');")
  if ([int]$got['innodb_flush_log_at_trx_commit'] -ne $FlushLog) { throw "flush_log not applied: $($got['innodb_flush_log_at_trx_commit'])" }
  if ([int]$got['sync_binlog'] -ne $SyncBinlog) { throw "sync_binlog not applied: $($got['sync_binlog'])" }
}

function Get-PromCommitHist {
  try {
    $body = (Invoke-WebRequest -UseBasicParsing -Uri $BackendMetricsUrl -TimeoutSec 5).Content
  } catch {
    return $null
  }
  $buckets = @()
  $count = 0.0
  $sum = 0.0
  foreach ($line in ($body -split "`n")) {
    if ($line -match 'ticket_order_consumer_stage_duration_seconds_bucket\{stage="commit",le="([^"]+)"\}\s+([0-9.eE+-]+)') {
      $le = $Matches[1]
      $c = [double]$Matches[2]
      $leNum = if ($le -eq '+Inf') { [double]::PositiveInfinity } else { [double]$le }
      $buckets += [pscustomobject]@{ le = $leNum; c = $c }
    } elseif ($line -match 'ticket_order_consumer_stage_duration_seconds_count\{stage="commit"\}\s+([0-9.eE+-]+)') {
      $count = [double]$Matches[1]
    } elseif ($line -match 'ticket_order_consumer_stage_duration_seconds_sum\{stage="commit"\}\s+([0-9.eE+-]+)') {
      $sum = [double]$Matches[1]
    } elseif ($line -match 'ticket_order_consumer_transaction_duration_seconds_count\{result="success"\}\s+([0-9.eE+-]+)') {
      # keep for optional use
    }
  }
  $buckets = @($buckets | Sort-Object le)
  return [pscustomobject]@{ buckets = $buckets; count = $count; sum = $sum }
}

function Hist-DeltaSummary($before, $after) {
  if ($null -eq $before -or $null -eq $after) { return $null }
  $dCount = $after.count - $before.count
  $dSum = $after.sum - $before.sum
  if ($dCount -le 0) { return [ordered]@{ count = 0 } }
  # Reconstruct approx samples via bucket deltas for percentile
  $edges = @()
  $prevB = 0.0; $prevA = 0.0
  $cum = New-Object System.Collections.Generic.List[object]
  for ($i = 0; $i -lt $after.buckets.Count; $i++) {
    $le = $after.buckets[$i].le
    $b = ($before.buckets | Where-Object { $_.le -eq $le } | Select-Object -First 1)
    $bc = if ($b) { $b.c } else { 0 }
    $delta = $after.buckets[$i].c - $bc
    $cum.Add([pscustomobject]@{ le = $le; c = $delta })
  }
  # cumulative already in prometheus buckets; need delta of cumulatives
  $deltaBuckets = @()
  $prev = 0.0
  foreach ($row in ($cum | Sort-Object le)) {
    # row.c is already absolute cumulative delta if before/after are cumulative
    $deltaBuckets += $row
  }
  # Fix: prometheus buckets are cumulative; delta of cumulative is cumulative of new samples
  function PctFromCum($buckets, $count, $p) {
    $target = $count * ($p / 100.0)
    $prevLe = 0.0; $prevC = 0.0
    foreach ($b in $buckets) {
      if ($b.c -ge $target) {
        if ($b.le -lt 1e100) {
          if ($b.c -eq $prevC) { return $b.le * 1000.0 }
          $frac = ($target - $prevC) / ($b.c - $prevC)
          return ($prevLe + $frac * ($b.le - $prevLe)) * 1000.0
        }
        return $prevLe * 1000.0
      }
      if ($b.le -lt 1e100) { $prevLe = $b.le }
      $prevC = $b.c
    }
    return $null
  }
  # Recompute cumulative deltas properly
  $pairs = @{}
  foreach ($b in $before.buckets) { $pairs[[string]$b.le] = @{ b = $b.c; a = 0 } }
  foreach ($b in $after.buckets) {
    $k = [string]$b.le
    if (-not $pairs.ContainsKey($k)) { $pairs[$k] = @{ b = 0; a = $b.c } } else { $pairs[$k].a = $b.c }
  }
  $db = @()
  foreach ($k in ($pairs.Keys | Sort-Object { if ($_ -eq 'Infinity') { [double]::PositiveInfinity } else { [double]$_ } })) {
    $le = if ($k -eq 'Infinity') { [double]::PositiveInfinity } else { [double]$k }
    $db += [pscustomobject]@{ le = $le; c = $pairs[$k].a - $pairs[$k].b }
  }
  $db = @($db | Sort-Object le)
  [ordered]@{
    count = [int]$dCount
    avg_ms = [Math]::Round(($dSum / $dCount) * 1000.0, 3)
    p50_ms = [Math]::Round((PctFromCum $db $dCount 50), 3)
    p95_ms = [Math]::Round((PctFromCum $db $dCount 95), 3)
    p99_ms = [Math]::Round((PctFromCum $db $dCount 99), 3)
  }
}

function Measure-CommitCell([string]$Label, [int]$FlushLog, [int]$SyncBinlog) {
  $cellDir = Join-Path $out $Label
  New-Item -ItemType Directory -Force -Path $cellDir | Out-Null
  Write-Host "=== CELL $Label (flush_log=$FlushLog sync_binlog=$SyncBinlog) ===" -ForegroundColor Cyan
  Set-Durability $FlushLog $SyncBinlog
  Start-Sleep -Seconds 1

  # single-thread wall via one CALL
  $sw = [Diagnostics.Stopwatch]::StartNew()
  Mysql "CALL fsync_probe.probe_commits($SingleCommits);" | Out-Null
  $sw.Stop()
  $singleWall = [Math]::Round($sw.Elapsed.TotalMilliseconds / $SingleCommits, 3)

  # concurrent wall samples
  Mysql "TRUNCATE TABLE performance_schema.events_waits_summary_global_by_event_name;" | Out-Null
  $before = Parse-Status (Mysql "SHOW GLOBAL STATUS WHERE Variable_name IN ('Innodb_os_log_fsyncs','Innodb_data_fsyncs','Com_commit','Innodb_log_waits');")
  $t0 = Get-Date
  $jobs = 1..$ConcurrentWorkers | ForEach-Object {
    Start-Job -ScriptBlock {
      param($Container, $Seconds)
      $deadline = (Get-Date).AddSeconds($Seconds)
      $samples = New-Object System.Collections.Generic.List[double]
      $commits = 0
      while ((Get-Date) -lt $deadline) {
        $sw = [Diagnostics.Stopwatch]::StartNew()
        docker exec -e MYSQL_PWD=fuchang-it-root $Container mysql -uroot -N -B -e "CALL fsync_probe.probe_commits(20);" 2>$null | Out-Null
        $sw.Stop()
        $samples.Add($sw.Elapsed.TotalMilliseconds / 20.0)
        $commits += 20
      }
      return [pscustomobject]@{ samples = @($samples); commits = $commits }
    } -ArgumentList $MysqlContainer, $ConcurrentSeconds
  }
  $null = $jobs | Wait-Job
  $results = @($jobs | Receive-Job)
  $jobs | Remove-Job -Force
  $elapsed = ((Get-Date) - $t0).TotalSeconds
  $after = Parse-Status (Mysql "SHOW GLOBAL STATUS WHERE Variable_name IN ('Innodb_os_log_fsyncs','Innodb_data_fsyncs','Com_commit','Innodb_log_waits');")
  $all = @()
  $jobCommits = 0
  foreach ($r in $results) {
    $jobCommits += $r.commits
    foreach ($s in @($r.samples)) { $all += [double]$s }
  }
  $wall = Summarize $all
  $dC = $after['Com_commit'] - $before['Com_commit']
  $dL = $after['Innodb_os_log_fsyncs'] - $before['Innodb_os_log_fsyncs']
  $dD = $after['Innodb_data_fsyncs'] - $before['Innodb_data_fsyncs']

  $waits = Mysql @"
SELECT EVENT_NAME, COUNT_STAR,
 ROUND(SUM_TIMER_WAIT/1e12,4) total_s,
 ROUND(AVG_TIMER_WAIT/1e9,3) avg_ms,
 ROUND(MAX_TIMER_WAIT/1e9,3) max_ms
FROM performance_schema.events_waits_summary_global_by_event_name
WHERE COUNT_STAR > 0 AND EVENT_NAME IN (
  'wait/io/file/innodb/innodb_log_file',
  'wait/io/file/sql/binlog',
  'wait/io/file/innodb/innodb_data_file',
  'wait/io/file/innodb/innodb_dblwr_file'
)
ORDER BY SUM_TIMER_WAIT DESC;
"@
  $waits | Set-Content -Encoding utf8 (Join-Path $cellDir "waits.tsv")

  $redoTotal = $null; $binlogTotal = $null
  foreach ($line in @($waits)) {
    $p = @(($line -split "\s+") | Where-Object { $_ -ne "" })
    if ($p.Count -ge 3 -and $p[0] -eq 'wait/io/file/innodb/innodb_log_file') { $redoTotal = [double]$p[2] }
    if ($p.Count -ge 3 -and $p[0] -eq 'wait/io/file/sql/binlog') { $binlogTotal = [double]$p[2] }
  }

  $cell = [ordered]@{
    label = $Label
    innodb_flush_log_at_trx_commit = $FlushLog
    sync_binlog = $SyncBinlog
    single_thread_wall_ms_per_commit = $singleWall
    concurrent = [ordered]@{
      workers = $ConcurrentWorkers
      seconds = [Math]::Round($elapsed, 3)
      job_commits = $jobCommits
      commits_delta = $dC
      commits_per_sec = [Math]::Round($dC / $elapsed, 2)
      innodb_os_log_fsyncs_per_sec = [Math]::Round($dL / $elapsed, 2)
      innodb_data_fsyncs_per_sec = [Math]::Round($dD / $elapsed, 2)
      log_fsyncs_per_commit = [Math]::Round($dL / [Math]::Max($dC, 1), 3)
      wall_per_commit_ms = $wall
      redo_wait_total_s = $redoTotal
      binlog_wait_total_s = $binlogTotal
    }
  }
  $cell | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $cellDir "cell.json")
  return $cell
}

# Ensure probe procedure exists
docker exec -e MYSQL_PWD=fuchang-it-root $MysqlContainer sh -c @'
mysql -uroot <<'"'"'SQL'"'"'
CREATE DATABASE IF NOT EXISTS fsync_probe;
CREATE TABLE IF NOT EXISTS fsync_probe.t (id BIGINT PRIMARY KEY AUTO_INCREMENT, v INT NOT NULL) ENGINE=InnoDB;
DROP PROCEDURE IF EXISTS fsync_probe.probe_commits;
DELIMITER //
CREATE PROCEDURE fsync_probe.probe_commits(IN n INT)
BEGIN
  DECLARE i INT DEFAULT 0;
  WHILE i < n DO
    START TRANSACTION;
    INSERT INTO fsync_probe.t(v) VALUES (i);
    COMMIT;
    SET i = i + 1;
  END WHILE;
END //
DELIMITER ;
SQL
'@ | Out-Null

$cells = @(
  @{ label = "1plus1_baseline"; flush = 1; sync = 1; title = "1+1 安全基线" },
  @{ label = "2plus1_no_redo_fsync"; flush = 2; sync = 1; title = "2+1 去掉每事务 redo fsync" },
  @{ label = "1plus0_no_binlog_fsync"; flush = 1; sync = 0; title = "1+0 去掉每事务 binlog fsync" },
  @{ label = "2plus0_ceiling"; flush = 2; sync = 0; title = "2+0 接近持久化天花板" }
)

$matrix = @()
foreach ($c in $cells) {
  $matrix += Measure-CommitCell $c.label $c.flush $c.sync
}

# Restore safe baseline
Set-Durability 1 1
Write-Host "Restored durability to 1+1" -ForegroundColor DarkGreen

$matrix | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $out "matrix.json")
$matrixPath = Join-Path $out "matrix.json"
Write-Host "matrix json written: $matrixPath"

if ($IncludeConsumerSpot) {
  Write-Host "=== Consumer spot checks ===" -ForegroundColor Cyan
  $consumerRows = New-Object System.Collections.Generic.List[object]
  $baseUrl = "http://127.0.0.1:18580/api/v1"
  $env:BASE_URL = $baseUrl
  $env:K6_USERS = "3000"
  $env:RUSH_TOTAL_QUOTA = "200000"
  $env:RUSH_PER_USER_LIMIT = "20"
  $env:SETUP_CONCURRENCY = "100"
  $env:MYSQL_CONTAINER = $MysqlContainer
  $env:K6_JWT_SECRET = "fuchang-integration-test-secret-only"

  foreach ($c in $cells) {
    Write-Host ("consumer spot " + $c.label) -ForegroundColor Yellow
    Set-Durability $c.flush $c.sync
    $env:RUSH_LABEL = ("durability-" + $c.label + "-" + (Get-Date -Format HHmmss))
    $promBefore = Get-PromCommitHist
    & node (Join-Path $repo "tests\load\k6\prepare_rush_fixture_fast.mjs")
    if ($LASTEXITCODE -ne 0) { throw ("prepare failed for " + $c.label) }
    $runDir = Join-Path $out ("consumer-" + $c.label)
    New-Item -ItemType Directory -Force -Path $runDir | Out-Null
    $diag = Join-Path $repo "tests\load\k6\run_peak_diagnostic.ps1"
    & powershell -NoProfile -ExecutionPolicy Bypass -File $diag -Vus $ConsumerVus -Duration $ConsumerDuration -BaseUrl $baseUrl -MetricsUrl $BackendMetricsUrl -RabbitContainer "gofun-txprofile-rabbitmq-1" -MysqlContainer $MysqlContainer -OutputDir (Join-Path $OutputDir ("consumer-" + $c.label)) -K6InDocker -K6Network "gofun-txprofile_default"
    if ($LASTEXITCODE -ne 0) { throw ("diagnostic failed " + $c.label) }

    $started = Get-Date
    while (((Get-Date) - $started).TotalSeconds -lt $ConsumerDrainSeconds) {
      $q = docker exec gofun-txprofile-rabbitmq-1 rabbitmqctl list_queues name messages --quiet 2>$null
      $work = 0
      foreach ($line in @($q)) {
        $p = @(($line -split "\s+") | Where-Object { $_ -ne "" })
        if ($p.Count -ge 2 -and ($p[0] -eq "fuchang.it.order.queue" -or $p[0] -eq "fuchang.it.order.retry")) {
          $work += [int64]$p[1]
        }
      }
      $pending = [int64](Mysql "SELECT COUNT(*) FROM fuchang_ticketing_it.ticket_order_outbox WHERE status IN ('pending','publishing');")
      if ($work -eq 0 -and $pending -eq 0) { break }
      Start-Sleep -Seconds 2
    }
    $promAfter = Get-PromCommitHist
    $commit = Hist-DeltaSummary $promBefore $promAfter
    $row = [ordered]@{
      label = $c.label
      flush = $c.flush
      sync = $c.sync
      consumer_commit = $commit
      drain_seconds = [Math]::Round(((Get-Date) - $started).TotalSeconds, 1)
    }
    $row | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 (Join-Path $runDir "commit-delta.json")
    $consumerRows.Add($row) | Out-Null
  }
  Set-Durability 1 1
  $consumerRows | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 (Join-Path $out "consumer_matrix.json")
}

& node (Join-Path $repo "tests\load\render_durability_matrix.mjs") $out
Write-Host ("Output: " + $out) -ForegroundColor DarkGreen