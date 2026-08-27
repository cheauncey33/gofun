param(
  [string]$Rates = "400,600,800,1000,1200",
  [string]$Duration = "60s",
  [int]$ConsumerWorkers = 8,
  [int]$InventoryBucketCount = 32,
  [int]$OutboxPublishWorkers = 4,
  [int]$PostSampleSeconds = 15,
  [int]$InnoDBFlushLogAtTrxCommit = 1,
  [switch]$SkipLogBin,
  [string]$Project = "gofun-csweep",
  [string]$OutputDir = "tests/load/results/mixed-rate-sweep-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [switch]$KeepStack,
  [string]$K6Image = "grafana/k6:latest"
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$outputPath = Join-Path $repo $OutputDir
New-Item -ItemType Directory -Force -Path $outputPath | Out-Null
$spotScript = Join-Path $PSScriptRoot "run_mixed_spot.ps1"
$rateList = @($Rates.Split(",") | ForEach-Object { [int]$_.Trim() } | Where-Object { $_ -gt 0 })
if ($rateList.Count -eq 0) { throw "Rates empty" }

$rows = @()
foreach ($rate in $rateList) {
  Write-Host "===== mixed rate=$rate =====" -ForegroundColor Cyan
  $runDir = Join-Path $OutputDir ("rate-" + $rate)
  $args = @(
    "-Rate", $rate,
    "-Duration", $Duration,
    "-ConsumerWorkers", $ConsumerWorkers,
    "-InventoryBucketCount", $InventoryBucketCount,
    "-OutboxPublishWorkers", $OutboxPublishWorkers,
    "-PostSampleSeconds", $PostSampleSeconds,
    "-InnoDBFlushLogAtTrxCommit", $InnoDBFlushLogAtTrxCommit,
    "-Project", $Project,
    "-OutputDir", $runDir,
    "-ReuseStack",
    "-KeepStack",
    "-FastPrepare",
    "-K6InDocker",
    "-K6Image", $K6Image
  )
  if ($SkipLogBin) { $args += "-SkipLogBin" }
  & powershell -NoProfile -ExecutionPolicy Bypass -File $spotScript @args
  if ($LASTEXITCODE -ne 0) { throw "mixed spot failed at rate=$rate" }

  $summaryPath = Join-Path $repo (Join-Path $runDir "mixed-spot-summary.json")
  $lifePath = Join-Path $repo (Join-Path $runDir "lifecycle-samples.json")
  $row = Get-Content $summaryPath -Raw -Encoding utf8 | ConvertFrom-Json

  # Sustained backlog: last 15s of HTTP window, ready slope > 0 and mean ready >= 50
  $life = Get-Content $lifePath -Raw -Encoding utf8
  if ($life.Length -gt 0 -and [int][char]$life[0] -eq 0xFEFF) { $life = $life.Substring(1) }
  $samples = @(($life | ConvertFrom-Json) | Where-Object { $_.phase -eq "http" } | Sort-Object elapsed_seconds)
  $tail = @($samples | Where-Object { $_.elapsed_seconds -ge ([double]$samples[-1].elapsed_seconds - 15) })
  $readyMean = 0.0
  $readySlope = 0.0
  $readyEnd = 0
  if ($tail.Count -ge 2) {
    $readyMean = [Math]::Round(($tail | ForEach-Object { [double]$_.rabbitmq.order_ready } | Measure-Object -Average).Average, 1)
    $readyEnd = [int64]$tail[-1].rabbitmq.order_ready
    $dt = [Math]::Max(0.001, [double]$tail[-1].elapsed_seconds - [double]$tail[0].elapsed_seconds)
    $readySlope = [Math]::Round(([double]$tail[-1].rabbitmq.order_ready - [double]$tail[0].rabbitmq.order_ready) / $dt, 2)
  }
  $sustained = ($readySlope -gt 5 -and $readyMean -ge 50) -or ($readyEnd -ge 200 -and $readySlope -gt 0)

  $enriched = [pscustomobject]@{
    target_rate = $rate
    achieved_req_rate = [Math]::Round([double]$row.http.achieved_req_rate, 1)
    http_p50_ms = [Math]::Round([double]$row.http.p50_ms, 1)
    http_p95_ms = [Math]::Round([double]$row.http.p95_ms, 1)
    http_p99_ms = [Math]::Round([double]$row.http.p99_ms, 1)
    action_ok_rate = $row.http.action_ok_rate
    consumer_tx_per_sec = $row.consumer.complete_rate_during_http
    consumer_tx_p95_ms = $row.consumer.tx_p95_ms
    consumer_tx_p99_ms = $row.consumer.tx_p99_ms
    order_ready_peak = $row.queues.order_ready_peak
    order_ready_end_http = $row.queues.order_ready_end_http
    ready_tail_mean = $readyMean
    ready_tail_slope_per_s = $readySlope
    outbox_pending_peak = $row.queues.outbox_pending_peak
    pending_payment_delta = $row.pending_payment.delta_during_http
    sustained_mq_backlog = $sustained
    run_dir = $runDir
  }
  $enriched | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 (Join-Path $repo (Join-Path $runDir "sweep-row.json"))
  $rows += $enriched
  Write-Host ("rate={0} ach={1} p95={2} p99={3} cons={4} readyPeak={5} readySlope={6}/s sustained={7}" -f `
    $rate, $enriched.achieved_req_rate, $enriched.http_p95_ms, $enriched.http_p99_ms, `
    $enriched.consumer_tx_per_sec, $enriched.order_ready_peak, $enriched.ready_tail_slope_per_s, $sustained) -ForegroundColor Green
}

$rows | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 (Join-Path $outputPath "sweep-summary.json")

# Inflection: first rate with sustained backlog, and first rate where p95 >= 2x baseline (rate[0]) or p99 >= 2x
$baseline = $rows[0]
$mqKnee = $null
$latKnee = $null
foreach ($r in $rows) {
  if ($null -eq $mqKnee -and $r.sustained_mq_backlog) { $mqKnee = $r }
  if ($null -eq $latKnee -and (
      $r.http_p95_ms -ge [Math]::Max(50, $baseline.http_p95_ms * 2) -or
      $r.http_p99_ms -ge [Math]::Max(200, $baseline.http_p99_ms * 2)
    )) {
    $latKnee = $r
  }
}
$combined = $null
foreach ($r in $rows) {
  if ($r.sustained_mq_backlog -and (
      $r.http_p95_ms -ge [Math]::Max(50, $baseline.http_p95_ms * 2) -or
      $r.http_p99_ms -ge [Math]::Max(200, $baseline.http_p99_ms * 2)
    )) {
    $combined = $r
    break
  }
}

$md = New-Object System.Text.StringBuilder
[void]$md.AppendLine("# Mixed traffic rate sweep (v2)")
[void]$md.AppendLine("")
[void]$md.AppendLine(("Fixed: Consumer={0}, bucket={1}, outbox={2}, Duration={3}, model=mixed_traffic_v2, nobinlog+redo=1" -f $ConsumerWorkers, $InventoryBucketCount, $OutboxPublishWorkers, $Duration))
[void]$md.AppendLine("")
[void]$md.AppendLine("Criteria:")
[void]$md.AppendLine("- MQ sustained backlog: last-15s ready slope > 5/s and mean >= 50, or end ready >= 200 with slope > 0")
[void]$md.AppendLine("- Latency worse: HTTP P95 >= max(50, 2x baseline) or P99 >= max(200, 2x baseline)")
[void]$md.AppendLine("")
[void]$md.AppendLine("| target | ach rps | P50 | P95 | P99 | cons tx/s | ready peak | ready end | ready slope/s | sustained MQ | outbox peak | pp delta |")
[void]$md.AppendLine("| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | :---: | ---: | ---: |")
foreach ($r in $rows) {
  [void]$md.AppendLine(("| {0} | {1} | {2} | {3} | {4} | {5} | {6} | {7} | {8} | {9} | {10} | {11} |" -f `
    $r.target_rate, $r.achieved_req_rate, $r.http_p50_ms, $r.http_p95_ms, $r.http_p99_ms, `
    $r.consumer_tx_per_sec, $r.order_ready_peak, $r.order_ready_end_http, $r.ready_tail_slope_per_s, `
    $r.sustained_mq_backlog, $r.outbox_pending_peak, $r.pending_payment_delta))
}

$mqKneeText = if ($mqKnee) { "{0} rps (slope={1}/s, peak={2})" -f $mqKnee.target_rate, $mqKnee.ready_tail_slope_per_s, $mqKnee.order_ready_peak } else { "not triggered" }
$latKneeText = if ($latKnee) { "{0} rps (P95={1}, P99={2})" -f $latKnee.target_rate, $latKnee.http_p95_ms, $latKnee.http_p99_ms } else { "not triggered" }
$combinedText = if ($combined) { "{0} rps" -f $combined.target_rate } else { "not both together; use earlier of the two" }
$suggest = if ($combined) {
  [string]$combined.target_rate
} elseif ($mqKnee -and $latKnee) {
  [string]([Math]::Min([int]$mqKnee.target_rate, [int]$latKnee.target_rate))
} elseif ($mqKnee) {
  [string]$mqKnee.target_rate
} elseif ($latKnee) {
  [string]$latKnee.target_rate
} else {
  "not found"
}

[void]$md.AppendLine("")
[void]$md.AppendLine("## Knee")
[void]$md.AppendLine("")
[void]$md.AppendLine(("- baseline {0} rps: P95={1} ms, P99={2} ms, cons={3} tx/s" -f $baseline.target_rate, $baseline.http_p95_ms, $baseline.http_p99_ms, $baseline.consumer_tx_per_sec))
[void]$md.AppendLine(("- MQ sustained backlog starts: {0}" -f $mqKneeText))
[void]$md.AppendLine(("- latency worsens: {0}" -f $latKneeText))
[void]$md.AppendLine(("- MQ backlog + latency together: {0}" -f $combinedText))
[void]$md.AppendLine(("- suggested observation knee: {0} rps" -f $suggest))

$mdText = $md.ToString()
[System.IO.File]::WriteAllText((Join-Path $outputPath "sweep-summary.md"), $mdText, [System.Text.UTF8Encoding]::new($false))
@{
  baseline = $baseline
  mq_knee = $mqKnee
  latency_knee = $latKnee
  combined_knee = $combined
  rows = $rows
  model = "mixed_traffic_v2"
} | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $outputPath "sweep-enriched.json")

Write-Host $mdText
Write-Host ("Sweep summary: " + (Join-Path $outputPath "sweep-summary.md")) -ForegroundColor Green

if (-not $KeepStack) {
  $baseCompose = Join-Path $repo "tests\integration\docker-compose.ticketing.yml"
  $capacityCompose = Join-Path $repo "tests\load\docker-compose.capacity.yml"
  cmd /c "docker compose -p $Project -f `"$baseCompose`" -f `"$capacityCompose`" down -v --remove-orphans >NUL 2>&1"
}
