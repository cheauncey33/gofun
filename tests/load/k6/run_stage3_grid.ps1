param(
  [string]$Vus = "50,100,200",
  [string]$Duration = "15s",
  [int]$DrainSeconds = 90,
  [int]$K6Users = 3000,
  [int]$SetupConcurrency = 100,
  [int]$TotalQuota = 200000,
  [string]$OutputDir = "tests/load/results/stage3-grid-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [switch]$FastPrepare,
  [string]$K6Image = "grafana/k6:latest"
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$baseline = Join-Path $repo "tests\load\k6\run_full_chain_baseline.ps1"
$gridRoot = Join-Path $repo $OutputDir
New-Item -ItemType Directory -Force -Path $gridRoot | Out-Null

# 第一组固定 8 个订单消费者与 8 个 outbox publisher，用于只比较库存库拓扑；
# 第三组再增加消费者，判断分库后是否值得继续横向扩消费者。
$allRows = New-Object System.Collections.ArrayList
$caseIndex = 0
for ($casePosition = 0; $casePosition -lt 3; $casePosition++) {
  $caseIndex++
  switch ($casePosition) {
    0 { $caseName = "single-db-c8-p8"; $caseShard = $false; $caseConsumer = 8; $casePublisher = 8 }
    1 { $caseName = "inventory-db-c8-p8"; $caseShard = $true; $caseConsumer = 8; $casePublisher = 8 }
    2 { $caseName = "inventory-db-c12-p8"; $caseShard = $true; $caseConsumer = 12; $casePublisher = 8 }
  }
  $project = "gofun-stage3-$($caseIndex)-$([Guid]::NewGuid().ToString('N').Substring(0, 6))"
  $caseOutput = Join-Path $OutputDir $caseName
  $args = @(
    "-Project", $project,
    "-Vus", $Vus,
    "-Duration", $Duration,
    "-DrainSeconds", $DrainSeconds,
    "-K6Users", $K6Users,
    "-SetupConcurrency", $SetupConcurrency,
    "-TotalQuota", $TotalQuota,
    "-OrderConsumerWorkers", $caseConsumer,
    "-OutboxPublishWorkers", $casePublisher,
    "-InventoryMysqlPort", ("$([int]13428 + $caseIndex)"),
    "-BackendPort", ("$([int]18680 + $caseIndex)"),
    "-MysqlPort", ("$([int]13427 + $caseIndex)"),
    "-RedisPort", ("$([int]16500 + $caseIndex)"),
    "-RabbitPort", ("$([int]26173 + $caseIndex)"),
    "-RabbitManagementPort", ("$([int]36173 + $caseIndex)"),
    "-OutputDir", $caseOutput,
    "-InventoryBatchEnabled",
    "-K6InDocker",
    "-K6Image", $K6Image
  )
  if ($caseShard) { $args += "-InventoryShardEnabled" }
  if ($FastPrepare) { $args += "-FastPrepare" }

  Write-Host "=== stage3 grid: $caseName ===" -ForegroundColor Cyan
  & powershell -NoProfile -ExecutionPolicy Bypass -File $baseline @args
  if ($LASTEXITCODE -ne 0) { throw "stage3 grid case failed: $caseName" }

  $summaryPath = Join-Path (Join-Path $repo $caseOutput) "baseline-summary.json"
  $caseRows = Get-Content $summaryPath -Raw -Encoding utf8 | ConvertFrom-Json
  foreach ($row in $caseRows) {
    [void]$allRows.Add([pscustomobject]@{
      case = $caseName
      shard = $caseShard
      consumers = $caseConsumer
      publishers = $casePublisher
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
    })
  }
}

$allRows | ConvertTo-Json -Depth 12 | Set-Content -Encoding utf8 (Join-Path $gridRoot "grid-summary.json")
$md = @(
  "# Stage 3 inventory DB grid",
  "",
  "- generated_at: $(Get-Date -Format o)",
  "- k6 runs inside the Compose network; VUS: $Vus; duration: $Duration; drain timeout: ${DrainSeconds}s",
  "- each case uses a fresh Compose project and fresh volumes",
  "",
  "| case | VUS | consumers | HTTP req/s | success | 5xx | transport | p99 | HTTP-window work MQ peak | post-test work MQ peak | pending inventory | drain | dead letters | lock waits |",
  "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|"
)
foreach ($row in $allRows) {
  $md += "| $($row.case) | $($row.vus) | $($row.consumers) | $([Math]::Round([double]$row.http_req_rate, 1)) | $([Math]::Round([double]$row.http_success_rate * 100, 2))% | $($row.server_errors) | $($row.transport_errors) | $([Math]::Round([double]$row.p99_ms, 1))ms | $($row.http_window_work_queue_peak) | $($row.post_test_work_queue_peak) | $($row.inventory_pending_after) | $([Math]::Round([double]$row.drain_seconds, 1))s / $($row.primary_drained) | $($row.dead_letters_after) | $($row.lock_waits_delta) |"
}
$md += ""
$md += "Raw case artifacts: $OutputDir/"
$md | Set-Content -Encoding utf8 (Join-Path $gridRoot "grid-summary.md")
Write-Host "Stage3 grid summary: $(Join-Path $gridRoot 'grid-summary.md')" -ForegroundColor Green
