param(
  [string]$Root = "tests/load/results/stage3-grid-20260810-final2"
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$rootPath = Join-Path $repo $Root
$allRows = New-Object System.Collections.ArrayList

function Get-MetricValue {
  param($Metrics, [string]$Name)
  $property = $Metrics.PSObject.Properties[$Name]
  if ($null -eq $property) { return 0.0 }
  return [double]$property.Value
}

function Get-WindowMetric {
  param($Samples, [string]$Name)
  if ($Samples.Count -lt 2) { return 0.0 }
  $firstValue = Get-MetricValue $Samples[0].prometheus $Name
  $lastValue = Get-MetricValue $Samples[-1].prometheus $Name
  return $lastValue - $firstValue
}

function Get-TransactionP95Ms {
  param($Samples)
  if ($Samples.Count -lt 2) { return 0.0 }
  $first = $Samples[0].prometheus
  $last = $Samples[-1].prometheus
  $buckets = @()
  foreach ($property in $last.PSObject.Properties) {
    if ($property.Name -notlike 'ticket_order_consumer_transaction_duration_seconds_bucket{*') { continue }
    if ($property.Name -notmatch 'result="success",le="([^"]+)"') { continue }
    $leText = $Matches[1]
    $upper = if ($leText -eq '+Inf') { [double]::PositiveInfinity } else { [double]$leText }
    $firstValue = Get-MetricValue $first $property.Name
    $delta = [double]$property.Value - $firstValue
    if ($delta -gt 0) {
      $buckets += [pscustomobject]@{ upper = $upper; delta = $delta }
    }
  }
  if ($buckets.Count -eq 0) { return 0.0 }
  $buckets = @($buckets | Sort-Object upper)
  # Prometheus histogram buckets are cumulative. The +Inf bucket is the
  # window total; finite buckets already contain the cumulative count.
  $totalBucket = $buckets | Where-Object { [double]::IsPositiveInfinity($_.upper) } | Select-Object -First 1
  if ($null -eq $totalBucket -or $totalBucket.delta -le 0) { return 0.0 }
  $target = $totalBucket.delta * 0.95
  foreach ($bucket in ($buckets | Where-Object { -not [double]::IsPositiveInfinity($_.upper) })) {
    if ($bucket.delta -ge $target) {
      return $bucket.upper * 1000
    }
  }
  return 0.0
}

$cases = @(
  @{ Name = "single-db-c8-p8"; Shard = $false; Consumers = 8; Publishers = 8 },
  @{ Name = "inventory-db-c8-p8"; Shard = $true; Consumers = 8; Publishers = 8 },
  @{ Name = "inventory-db-c12-p8"; Shard = $true; Consumers = 12; Publishers = 8 }
)

foreach ($case in $cases) {
  $summaryPath = Join-Path (Join-Path $rootPath $case.Name) "baseline-summary.json"
  if (-not (Test-Path $summaryPath)) { throw "missing case summary: $summaryPath" }
  $rows = Get-Content $summaryPath -Raw -Encoding utf8 | ConvertFrom-Json
  foreach ($row in $rows) {
    $runPath = Join-Path (Join-Path (Join-Path $rootPath $case.Name) ("vus-" + $row.vus)) "samples.json"
    $samples = Get-Content $runPath -Raw -Encoding utf8 | ConvertFrom-Json
    $sampleSeconds = ([DateTimeOffset]::Parse($samples[-1].timestamp) - [DateTimeOffset]::Parse($samples[0].timestamp)).TotalSeconds
    if ($sampleSeconds -le 0) { $sampleSeconds = 1 }
    $consumedCount = Get-WindowMetric $samples 'mq_messages_consumed_total{result="success"}'
    $txCount = Get-WindowMetric $samples 'ticket_order_consumer_transactions_total{result="success"}'
    $txSum = Get-WindowMetric $samples 'ticket_order_consumer_transaction_duration_seconds_sum{result="success"}'
    $batchCount = Get-WindowMetric $samples 'inventory_reservation_batches_total{result="success"}'
    [void]$allRows.Add([pscustomobject]@{
      case = $case.Name
      shard = $case.Shard
      consumers = $case.Consumers
      publishers = $case.Publishers
      vus = $row.vus
      http_req_rate = $row.http_req_rate
      http_success_rate = $row.http_success_rate
      transport_errors = $row.transport_errors
      server_errors = $row.server_errors
      p99_ms = $row.p99_ms
      http_window_work_queue_peak = $row.http_window_work_queue_peak
      post_test_work_queue_peak = $row.post_test_work_queue_peak
      drain_seconds = $row.drain_seconds
      primary_drained = $row.primary_drained
      dead_letters_after = $row.dead_letters_after
      pending_outbox_after = $row.outbox_pending_after
      inventory_pending_after = $row.inventory_pending_after
      inventory_operations_after = $row.inventory_operations_after
      lock_waits_delta = $row.lock_waits_delta
      consumer_rate = $consumedCount / $sampleSeconds
      tx_count = $txCount
      tx_avg_ms = if ($txCount -gt 0) { $txSum / $txCount * 1000 } else { 0 }
      tx_p95_ms = Get-TransactionP95Ms $samples
      inventory_batch_rate = $batchCount / $sampleSeconds
    })
  }
}

$allRows | ConvertTo-Json -Depth 12 | Set-Content -Encoding utf8 (Join-Path $rootPath "grid-summary.json")
$md = @(
  "# Stage 3 inventory DB grid",
  "",
  "- generated_at: $(Get-Date -Format o)",
  "- k6 runs inside the Compose network; VUS: 50,100,200; duration: 15s; drain timeout: 90s",
  "- each case used a fresh Compose project and fresh volumes",
  "",
  "| case | VUS | consumers | HTTP req/s | consumer msg/s | tx avg/P95 | success | 5xx | p99 | HTTP-window work MQ peak | post-test work MQ peak | pending inventory | drain | dead letters | lock waits |",
  "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|"
)
foreach ($row in $allRows) {
  $md += "| $($row.case) | $($row.vus) | $($row.consumers) | $([Math]::Round([double]$row.http_req_rate, 1)) | $([Math]::Round([double]$row.consumer_rate, 1)) | $([Math]::Round([double]$row.tx_avg_ms, 1)) / $([Math]::Round([double]$row.tx_p95_ms, 1))ms | $([Math]::Round([double]$row.http_success_rate * 100, 2))% | $($row.server_errors) | $([Math]::Round([double]$row.p99_ms, 1))ms | $($row.http_window_work_queue_peak) | $($row.post_test_work_queue_peak) | $($row.inventory_pending_after) | $([Math]::Round([double]$row.drain_seconds, 1))s / $($row.primary_drained) | $($row.dead_letters_after) | $($row.lock_waits_delta) |"
}
$md += ""
$md += "Raw case artifacts: $Root/"
$md | Set-Content -Encoding utf8 (Join-Path $rootPath "grid-summary.md")
Write-Host "Merged stage3 grid: $(Join-Path $rootPath 'grid-summary.md')" -ForegroundColor Green
