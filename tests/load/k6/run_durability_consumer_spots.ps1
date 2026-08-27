param(
  [string]$MysqlContainer = "gofun-txprofile-mysql-1",
  [string]$OutputDir = "tests/load/results/durability-matrix-20260810",
  [int]$Vus = 300,
  [string]$Duration = "15s",
  [int]$DrainSeconds = 180
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$out = Join-Path $repo $OutputDir
$metricsUrl = "http://127.0.0.1:18580/metrics"
$baseUrl = "http://127.0.0.1:18580/api/v1"

function Mysql([string]$Sql) {
  docker exec -e MYSQL_PWD=fuchang-it-root $MysqlContainer mysql -uroot -N -B -e $Sql 2>$null
}

function Set-Durability([int]$FlushLog, [int]$SyncBinlog) {
  Mysql "SET GLOBAL innodb_flush_log_at_trx_commit=$FlushLog; SET GLOBAL sync_binlog=$SyncBinlog;"
  $v = Mysql "SHOW VARIABLES WHERE Variable_name IN ('innodb_flush_log_at_trx_commit','sync_binlog');"
  Write-Host $v
}

function Get-PromMap {
  $body = (Invoke-WebRequest -UseBasicParsing -Uri $metricsUrl -TimeoutSec 5).Content
  $map = @{}
  foreach ($line in ($body -split "`n")) {
    if ($line -match '^(ticket_order_consumer_stage_duration_seconds_(?:bucket|sum|count)\{[^}]+\}|ticket_order_consumer_stage_duration_seconds_(?:sum|count)\{[^}]+\})\s+([0-9.eE+-]+)$' `
      -or $line -match '^(ticket_order_consumer_stage_duration_seconds_bucket\{stage="commit",le="[^"]+"\})\s+([0-9.eE+-]+)$' `
      -or $line -match '^(ticket_order_consumer_stage_duration_seconds_(?:count|sum)\{stage="commit"\})\s+([0-9.eE+-]+)$') {
      # fallthrough
    }
    if ($line -match '^(ticket_order_consumer_[^\s]+)\s+([0-9.eE+-]+)$') {
      $map[$Matches[1]] = [double]$Matches[2]
    }
  }
  return $map
}

function Commit-SummaryFromDelta($before, $after) {
  $bCount = 0.0; $aCount = 0.0; $bSum = 0.0; $aSum = 0.0
  $bBuckets = @{}; $aBuckets = @{}
  foreach ($k in $before.Keys) {
    if ($k -match 'stage_duration_seconds_count\{stage="commit"\}') { $bCount = $before[$k] }
    if ($k -match 'stage_duration_seconds_sum\{stage="commit"\}') { $bSum = $before[$k] }
    if ($k -match 'stage_duration_seconds_bucket\{stage="commit",le="([^"]+)"\}') { $bBuckets[$Matches[1]] = $before[$k] }
  }
  foreach ($k in $after.Keys) {
    if ($k -match 'stage_duration_seconds_count\{stage="commit"\}') { $aCount = $after[$k] }
    if ($k -match 'stage_duration_seconds_sum\{stage="commit"\}') { $aSum = $after[$k] }
    if ($k -match 'stage_duration_seconds_bucket\{stage="commit",le="([^"]+)"\}') { $aBuckets[$Matches[1]] = $after[$k] }
  }
  $count = $aCount - $bCount
  $sum = $aSum - $bSum
  if ($count -le 0) { return [ordered]@{ count = 0 } }
  $edges = @($aBuckets.Keys | ForEach-Object { if ($_ -eq '+Inf') { [double]::PositiveInfinity } else { [double]$_ } } | Sort-Object)
  $cum = @()
  foreach ($le in $edges) {
    $key = if ($le -gt 1e100) { '+Inf' } else { ([string]$le) }
    # prometheus may stringify 0.001 without scientific; match by parsing
    $ak = ($aBuckets.Keys | Where-Object {
      $v = $_; if ($v -eq '+Inf') { return $le -gt 1e100 }; [double]$v -eq $le
    } | Select-Object -First 1)
    $bk = ($bBuckets.Keys | Where-Object {
      $v = $_; if ($v -eq '+Inf') { return $le -gt 1e100 }; [double]$v -eq $le
    } | Select-Object -First 1)
    $av = if ($ak) { $aBuckets[$ak] } else { 0 }
    $bv = if ($bk) { $bBuckets[$bk] } else { 0 }
    $cum += [pscustomobject]@{ le = $le; c = $av - $bv }
  }
  function Pct($buckets, $count, $p) {
    $target = $count * ($p / 100.0)
    $prevLe = 0.0; $prevC = 0.0
    foreach ($b in $buckets) {
      if ($b.c -ge $target) {
        if ($b.le -gt 1e100) { return [Math]::Round($prevLe * 1000, 3) }
        if ($b.c -eq $prevC) { return [Math]::Round($b.le * 1000, 3) }
        $frac = ($target - $prevC) / ($b.c - $prevC)
        return [Math]::Round(($prevLe + $frac * ($b.le - $prevLe)) * 1000, 3)
      }
      if ($b.le -lt 1e100) { $prevLe = $b.le }
      $prevC = $b.c
    }
    return $null
  }
  [ordered]@{
    count = [int]$count
    avg_ms = [Math]::Round(($sum / $count) * 1000, 3)
    p50_ms = Pct $cum $count 50
    p95_ms = Pct $cum $count 95
    p99_ms = Pct $cum $count 99
  }
}

$cells = @(
  @{ label = "1plus1_baseline"; flush = 1; sync = 1 },
  @{ label = "2plus1_no_redo_fsync"; flush = 2; sync = 1 },
  @{ label = "1plus0_no_binlog_fsync"; flush = 1; sync = 0 },
  @{ label = "2plus0_ceiling"; flush = 2; sync = 0 }
)

$env:BASE_URL = $baseUrl
$env:K6_USERS = "3000"
$env:RUSH_TOTAL_QUOTA = "200000"
$env:RUSH_PER_USER_LIMIT = "20"
$env:SETUP_CONCURRENCY = "100"
$env:MYSQL_CONTAINER = $MysqlContainer
$env:K6_JWT_SECRET = "fuchang-integration-test-secret-only"

$rows = New-Object System.Collections.Generic.List[object]
foreach ($c in $cells) {
  Write-Host ("=== consumer " + $c.label + " ===") -ForegroundColor Cyan
  Set-Durability $c.flush $c.sync
  $env:RUSH_LABEL = ("durability-" + $c.label + "-" + (Get-Date -Format HHmmss))
  $before = Get-PromMap
  & node (Join-Path $repo "tests\load\k6\prepare_rush_fixture_fast.mjs")
  if ($LASTEXITCODE -ne 0) { throw "prepare failed" }
  $rel = Join-Path $OutputDir ("consumer-" + $c.label)
  $abs = Join-Path $repo $rel
  New-Item -ItemType Directory -Force -Path $abs | Out-Null
  & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $repo "tests\load\k6\run_peak_diagnostic.ps1") `
    -Vus $Vus -Duration $Duration -BaseUrl $baseUrl -MetricsUrl $metricsUrl `
    -RabbitContainer "gofun-txprofile-rabbitmq-1" -MysqlContainer $MysqlContainer `
    -OutputDir $rel -K6InDocker -K6Network "gofun-txprofile_default"
  if ($LASTEXITCODE -ne 0) { throw "k6 failed" }

  $started = Get-Date
  while (((Get-Date) - $started).TotalSeconds -lt $DrainSeconds) {
    $q = docker exec gofun-txprofile-rabbitmq-1 rabbitmqctl list_queues name messages --quiet 2>$null
    $work = 0
    foreach ($line in @($q)) {
      $p = @(($line -split "\s+") | Where-Object { $_ -ne "" })
      if ($p.Count -ge 2 -and ($p[0] -eq "fuchang.it.order.queue" -or $p[0] -eq "fuchang.it.order.retry")) { $work += [int64]$p[1] }
    }
    $pending = [int64](Mysql "SELECT COUNT(*) FROM fuchang_ticketing_it.ticket_order_outbox WHERE status IN ('pending','publishing');")
    if ($work -eq 0 -and $pending -eq 0) { break }
    Start-Sleep -Seconds 2
  }
  $after = Get-PromMap
  $commit = Commit-SummaryFromDelta $before $after
  $row = [ordered]@{
    label = $c.label
    flush = $c.flush
    sync = $c.sync
    consumer_commit = $commit
    drain_seconds = [Math]::Round(((Get-Date) - $started).TotalSeconds, 1)
  }
  $row | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 (Join-Path $abs "commit-delta.json")
  $rows.Add([pscustomobject]$row) | Out-Null
  Write-Host ($row | ConvertTo-Json -Compress)
}

Set-Durability 1 1
$rows | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 (Join-Path $out "consumer_matrix.json")
& node (Join-Path $repo "tests\load\render_durability_matrix.mjs") $out
Write-Host ("done " + $out)
