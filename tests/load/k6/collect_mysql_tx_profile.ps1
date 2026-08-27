param(
  [string]$MysqlContainer = "gofun-txprofile-mysql-1",
  [string]$OutputDir = "tests/load/results/tx-profile-mysql",
  [string]$CampaignId = ""
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$outPath = Join-Path $repo $OutputDir
New-Item -ItemType Directory -Force -Path $outPath | Out-Null

function Invoke-Mysql {
  param([string]$Query, [string]$User = "root", [string]$Password = "fuchang-it-root")
  $args = @("exec", "-e", "MYSQL_PWD=$Password", $MysqlContainer, "mysql", "-u$user", "-N", "-B", "-e", $Query)
  & docker @args 2>$null
}

function Invoke-MysqlFile {
  param([string]$Query, [string]$FileName, [string]$User = "root", [string]$Password = "fuchang-it-root")
  $tmp = [System.IO.Path]::GetTempFileName()
  Set-Content -Path $tmp -Value $Query -Encoding utf8
  # Use docker exec with -e for password; pipe SQL via stdin
  $bytes = [System.Text.Encoding]::UTF8.GetBytes($Query)
  $psi = New-Object System.Diagnostics.ProcessStartInfo
  $psi.FileName = "docker"
  $psi.Arguments = "exec -i -e MYSQL_PWD=$Password $MysqlContainer mysql -u$User -B"
  $psi.RedirectStandardInput = $true
  $psi.RedirectStandardOutput = $true
  $psi.RedirectStandardError = $true
  $psi.UseShellExecute = $false
  $p = [System.Diagnostics.Process]::Start($psi)
  $p.StandardInput.Write($Query)
  $p.StandardInput.Close()
  $stdout = $p.StandardOutput.ReadToEnd()
  $stderr = $p.StandardError.ReadToEnd()
  $p.WaitForExit()
  $stdout | Set-Content -Encoding utf8 (Join-Path $outPath $FileName)
  if ($stderr) { $stderr | Set-Content -Encoding utf8 (Join-Path $outPath ($FileName + ".err")) }
  Remove-Item $tmp -ErrorAction SilentlyContinue
}

# Enable statement history consumers
Invoke-Mysql @"
UPDATE performance_schema.setup_consumers
SET ENABLED = 'YES'
WHERE NAME IN ('events_statements_current','events_statements_history','events_statements_history_long','statements_digest');
"@ | Out-Null

$indexSQL = @"
SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = 'fuchang_ticketing_it'
  AND TABLE_NAME IN ('ticket_order','ticket_order_item','rush_campaign_bucket','ticket_tier_bucket','ticket_tier','rush_sale_campaign')
ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX;
"@
Invoke-MysqlFile $indexSQL "indexes.tsv"

$digestSQL = @"
SELECT
  DIGEST_TEXT,
  COUNT_STAR,
  ROUND(SUM_TIMER_WAIT/1e12, 4) AS total_s,
  ROUND(AVG_TIMER_WAIT/1e9, 3) AS avg_ms,
  ROUND(MAX_TIMER_WAIT/1e9, 3) AS max_ms,
  ROUND(SUM_LOCK_TIME/1e12, 4) AS lock_s,
  SUM_ROWS_EXAMINED,
  SUM_ROWS_AFFECTED,
  SUM_ROWS_SENT,
  SUM_NO_INDEX_USED,
  SUM_CREATED_TMP_TABLES,
  SUM_SORT_ROWS
FROM performance_schema.events_statements_summary_by_digest
WHERE SCHEMA_NAME = 'fuchang_ticketing_it'
  AND (
    DIGEST_TEXT LIKE '%ticket_order%'
    OR DIGEST_TEXT LIKE '%ticket_order_item%'
    OR DIGEST_TEXT LIKE '%rush_campaign_bucket%'
    OR DIGEST_TEXT LIKE '%ticket_tier_bucket%'
    OR DIGEST_TEXT LIKE '%COMMIT%'
    OR DIGEST_TEXT LIKE '%BEGIN%'
    OR DIGEST_TEXT LIKE '%START TRANSACTION%'
  )
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 40;
"@
Invoke-MysqlFile $digestSQL "statement_digest_top.tsv"

$waitSQL = @"
SELECT EVENT_NAME,
       COUNT_STAR,
       ROUND(SUM_TIMER_WAIT/1e12, 4) AS total_s,
       ROUND(AVG_TIMER_WAIT/1e9, 3) AS avg_ms,
       ROUND(MAX_TIMER_WAIT/1e9, 3) AS max_ms
FROM performance_schema.events_waits_summary_global_by_event_name
WHERE COUNT_STAR > 0
  AND EVENT_NAME NOT LIKE 'idle%'
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 40;
"@
Invoke-MysqlFile $waitSQL "wait_events_top.tsv"

$ioSQL = @"
SHOW GLOBAL STATUS WHERE Variable_name IN (
  'Innodb_row_lock_waits','Innodb_row_lock_time','Innodb_row_lock_time_avg','Innodb_row_lock_current_waits',
  'Innodb_log_waits','Innodb_os_log_fsyncs','Innodb_os_log_pending_fsyncs','Innodb_os_log_pending_writes',
  'Innodb_data_fsyncs','Innodb_data_pending_fsyncs','Innodb_data_pending_reads','Innodb_data_pending_writes',
  'Innodb_buffer_pool_wait_free','Innodb_dblwr_writes','Binlog_cache_disk_use','Threads_running','Threads_connected'
);
SHOW GLOBAL VARIABLES WHERE Variable_name IN (
  'innodb_flush_log_at_trx_commit','sync_binlog','innodb_flush_method','innodb_log_file_size',
  'innodb_log_buffer_size','binlog_order_commits','transaction_isolation','innodb_io_capacity'
);
"@
Invoke-MysqlFile $ioSQL "innodb_status_vars.tsv"

# EXPLAIN ANALYZE for the consumer SQL shapes (use any existing campaign/tier/order if present)
$sample = @(Invoke-Mysql "SELECT o.id, o.rush_sale_campaign_id, i.ticket_tier_id, o.stock_bucket_no, o.rush_bucket_no FROM fuchang_ticketing_it.ticket_order o JOIN fuchang_ticketing_it.ticket_order_item i ON i.order_id=o.id WHERE o.rush_sale_campaign_id IS NOT NULL ORDER BY o.id DESC LIMIT 1;" "fuchang" "fuchang-it-mysql")
if ($sample.Count -gt 0 -and $sample[0]) {
  $parts = ($sample[0] -split "\s+") | Where-Object { $_ -ne "" }
  if ($parts.Count -ge 5) {
    $orderId = $parts[0]
    $campaignId = $parts[1]
    $tierId = $parts[2]
    $stockBucket = $parts[3]
    $rushBucket = $parts[4]
    if ([string]::IsNullOrWhiteSpace($stockBucket) -or $stockBucket -eq "NULL") { $stockBucket = "0" }
    if ([string]::IsNullOrWhiteSpace($rushBucket) -or $rushBucket -eq "NULL") { $rushBucket = "0" }

    $explainSQL = @"
EXPLAIN ANALYZE SELECT id, user_id, status, rush_sale_campaign_id, stock_bucket_no, rush_bucket_no
FROM fuchang_ticketing_it.ticket_order WHERE id = $orderId FOR UPDATE;
EXPLAIN ANALYZE SELECT ticket_tier_id, quantity FROM fuchang_ticketing_it.ticket_order_item WHERE order_id = $orderId LIMIT 2;
EXPLAIN ANALYZE SELECT remaining_quota FROM fuchang_ticketing_it.rush_campaign_bucket
WHERE campaign_id = $campaignId AND bucket_no = $rushBucket AND remaining_quota >= 1 FOR UPDATE;
EXPLAIN ANALYZE SELECT remaining_quota, sold_count, version FROM fuchang_ticketing_it.ticket_tier_bucket
WHERE tier_id = $tierId AND bucket_no = $stockBucket AND remaining_quota >= 1 FOR UPDATE;
EXPLAIN ANALYZE SELECT remaining_quota FROM fuchang_ticketing_it.ticket_tier_bucket
WHERE tier_id = $tierId AND bucket_no = $stockBucket;
EXPLAIN ANALYZE SELECT id, status FROM fuchang_ticketing_it.ticket_order WHERE id = $orderId AND status = 'queued' FOR UPDATE;
"@
    Invoke-MysqlFile $explainSQL "explain_analyze.txt"
  }
}

Write-Host "MySQL tx profile written to $outPath" -ForegroundColor DarkGreen
