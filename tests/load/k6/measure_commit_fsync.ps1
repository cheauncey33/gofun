param(
  [string]$MysqlContainer = "gofun-txprofile-mysql-1",
  [string]$OutputDir = "tests/load/results/tx-profile-c6b32-20260810/fsync-probe",
  [int]$FsyncSamples = 200,
  [int]$CommitSamples = 300,
  [int]$CommitConcurrency = 6,
  [int]$BusySeconds = 30
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$out = Join-Path $repo $OutputDir
New-Item -ItemType Directory -Force -Path $out | Out-Null

function MysqlFile([string]$Sql) {
  $tmp = Join-Path $out ("q-" + [guid]::NewGuid().ToString("n") + ".sql")
  Set-Content -Path $tmp -Value $Sql -Encoding ascii
  docker cp $tmp "${MysqlContainer}:/tmp/probe.sql" | Out-Null
  docker exec -e MYSQL_PWD=fuchang-it-root $MysqlContainer mysql -uroot -N -B < $tmp 2>$null
  # PowerShell redirect into docker exec is unreliable; use sh -c with file inside container
  docker exec -e MYSQL_PWD=fuchang-it-root $MysqlContainer sh -c "mysql -uroot -N -B < /tmp/probe.sql" 2>$null
}

function Mysql([string]$Sql) {
  docker exec -e MYSQL_PWD=fuchang-it-root $MysqlContainer mysql -uroot -N -B -e $Sql 2>$null
}

function Percentile([double[]]$sorted, [double]$p) {
  if ($sorted.Count -eq 0) { return $null }
  if ($sorted.Count -eq 1) { return $sorted[0] }
  $rank = ($p / 100.0) * ($sorted.Count - 1)
  $lo = [int][Math]::Floor($rank)
  $hi = [int][Math]::Ceiling($rank)
  if ($lo -eq $hi) { return $sorted[$lo] }
  $w = $rank - $lo
  return $sorted[$lo] * (1 - $w) + $sorted[$hi] * $w
}

function SummarizeMs([double[]]$values) {
  if ($null -eq $values -or $values.Count -eq 0) {
    return [pscustomobject]@{ count = 0; avg_ms = $null; p50_ms = $null; p95_ms = $null; p99_ms = $null; min_ms = $null; max_ms = $null }
  }
  $sorted = @($values | Sort-Object)
  $sum = ($sorted | Measure-Object -Sum).Sum
  [pscustomobject]@{
    count = $sorted.Count
    avg_ms = [Math]::Round($sum / $sorted.Count, 3)
    p50_ms = [Math]::Round((Percentile $sorted 50), 3)
    p95_ms = [Math]::Round((Percentile $sorted 95), 3)
    p99_ms = [Math]::Round((Percentile $sorted 99), 3)
    min_ms = [Math]::Round($sorted[0], 3)
    max_ms = [Math]::Round($sorted[-1], 3)
  }
}

function Parse-StatusMap([string[]]$rows) {
  $map = @{}
  foreach ($r in @($rows)) {
    if (-not $r) { continue }
    $parts = @(($r -split "\s+") | Where-Object { $_ -ne "" })
    if ($parts.Count -ge 2 -and $parts[1] -match '^\d+(\.\d+)?$') {
      $map[$parts[0]] = [double]$parts[1]
    }
  }
  return $map
}

Write-Host "=== durability ===" -ForegroundColor Cyan
$vars = Mysql "SHOW VARIABLES WHERE Variable_name IN ('innodb_flush_log_at_trx_commit','sync_binlog','innodb_flush_method','datadir','version','binlog_order_commits');"
$vars | Set-Content -Encoding utf8 (Join-Path $out "mysql_vars.tsv")
$vars

Write-Host "=== setup probe schema ===" -ForegroundColor Cyan
Mysql "CREATE DATABASE IF NOT EXISTS fsync_probe;"
Mysql "CREATE TABLE IF NOT EXISTS fsync_probe.t (id BIGINT PRIMARY KEY AUTO_INCREMENT, v INT NOT NULL) ENGINE=InnoDB;"
$procSql = @"
DROP PROCEDURE IF EXISTS fsync_probe.probe_commits;
CREATE PROCEDURE fsync_probe.probe_commits(IN n INT)
BEGIN
  DECLARE i INT DEFAULT 0;
  WHILE i < n DO
    START TRANSACTION;
    INSERT INTO fsync_probe.t(v) VALUES (i);
    COMMIT;
    SET i = i + 1;
  END WHILE;
END
"@
Set-Content -Path (Join-Path $out "create_proc.sql") -Value $procSql -Encoding ascii
docker cp (Join-Path $out "create_proc.sql") "${MysqlContainer}:/tmp/create_proc.sql" | Out-Null
docker exec -e MYSQL_PWD=fuchang-it-root $MysqlContainer sh -c "mysql -uroot < /tmp/create_proc.sql" | Out-Null

Write-Host "=== 1) raw fsync on MySQL datadir volume ===" -ForegroundColor Cyan
$c = @'
#define _GNU_SOURCE
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>
static double now_ms(void){struct timespec ts; clock_gettime(CLOCK_MONOTONIC,&ts); return ts.tv_sec*1000.0+ts.tv_nsec/1e6;}
int cmp(const void*a,const void*b){double x=*(const double*)a,y=*(const double*)b; return (x>y)-(x<y);}
int main(int argc,char**argv){
  int n=argc>1?atoi(argv[1]):200; const char* path=argc>2?argv[2]:"/var/lib/mysql/fsync_probe_raw.bin";
  int fd=open(path,O_CREAT|O_RDWR|O_TRUNC,0644); if(fd<0){perror("open"); return 1;}
  char buf[4096]; memset(buf,'x',sizeof buf);
  if(write(fd,buf,sizeof buf)<0){perror("write"); return 1;} fsync(fd);
  double *lat=calloc(n,sizeof(double));
  for(int i=0;i<n;i++){
    lseek(fd,0,SEEK_SET); buf[0]=(char)(i&255); write(fd,buf,sizeof buf);
    double t0=now_ms(); fsync(fd); lat[i]=now_ms()-t0;
  }
  close(fd); unlink(path); qsort(lat,n,sizeof(double),cmp);
  double sum=0; for(int i=0;i<n;i++) sum+=lat[i];
  int i50=(int)((n-1)*0.50), i95=(int)((n-1)*0.95), i99=(int)((n-1)*0.99);
  printf("{\"path\":\"%s\",\"count\":%d,\"avg_ms\":%.6f,\"p50_ms\":%.6f,\"p95_ms\":%.6f,\"p99_ms\":%.6f,\"min_ms\":%.6f,\"max_ms\":%.6f}\n",
    path,n,sum/n,lat[i50],lat[i95],lat[i99],lat[0],lat[n-1]);
  return 0;
}
'@
Set-Content -Path (Join-Path $out "fsync_probe.c") -Value $c -Encoding ascii
docker cp (Join-Path $out "fsync_probe.c") "${MysqlContainer}:/tmp/fsync_probe.c" | Out-Null

# mysql image often lacks gcc; try installing build-essential is heavy. Use a sidecar alpine with volume mount instead.
$rawFsyncJson = $null
$compileOut = docker exec $MysqlContainer sh -c "command -v gcc; command -v cc; command -v python3; command -v perl" 2>$null
Write-Host "tools: $compileOut"

if ($compileOut -match "python3") {
  $py = @'
import os, time, json, sys
path = sys.argv[2] if len(sys.argv)>2 else "/var/lib/mysql/fsync_probe_raw.bin"
samples = int(sys.argv[1])
lat=[]
fd=os.open(path, os.O_CREAT|os.O_RDWR|os.O_TRUNC, 0o644)
os.write(fd, b"x"*4096); os.fsync(fd)
for i in range(samples):
    os.lseek(fd,0,os.SEEK_SET); os.write(fd, bytes([i%256])*4096)
    t0=time.perf_counter(); os.fsync(fd); lat.append((time.perf_counter()-t0)*1000)
os.close(fd)
try: os.remove(path)
except: pass
s=sorted(lat)
def pct(p):
    k=(len(s)-1)*(p/100.0); lo=int(k); hi=min(lo+1,len(s)-1); w=k-lo
    return s[lo]*(1-w)+s[hi]*w
print(json.dumps({"path":path,"count":len(s),"avg_ms":sum(s)/len(s),"p50_ms":pct(50),"p95_ms":pct(95),"p99_ms":pct(99),"min_ms":s[0],"max_ms":s[-1]}))
'@
  Set-Content -Path (Join-Path $out "fsync_probe.py") -Value $py -Encoding ascii
  docker cp (Join-Path $out "fsync_probe.py") "${MysqlContainer}:/tmp/fsync_probe.py" | Out-Null
  $rawFsyncJson = docker exec $MysqlContainer python3 /tmp/fsync_probe.py $FsyncSamples
} else {
  # Use docker run alpine sharing the same volume
  $vol = (docker inspect $MysqlContainer --format "{{range .Mounts}}{{if eq .Destination \"/var/lib/mysql\"}}{{.Name}}{{end}}{{end}}").Trim()
  if (-not $vol) { throw "cannot resolve mysql data volume" }
  Write-Host "using volume $vol with alpine+gcc"
  docker run --rm -v "${vol}:/data" alpine:3.20 sh -c "apk add --no-cache gcc musl-dev >/dev/null && cat > /tmp/fsync_probe.c <<'EOF'
$c
EOF
gcc -O2 -o /tmp/fsync_probe /tmp/fsync_probe.c && /tmp/fsync_probe $FsyncSamples /data/fsync_probe_raw.bin" | Tee-Object -Variable rawFsyncJson | Out-Null
  $rawFsyncJson = ($rawFsyncJson | Select-Object -Last 1)
}

$rawFsyncJson | Set-Content -Encoding utf8 (Join-Path $out "raw_fsync_datadir.json")
Write-Host "raw fsync: $rawFsyncJson"

Write-Host "=== 1b) raw fsync on host D: temp ===" -ForegroundColor Cyan
$hostFsync = @'
using System;
using System.Diagnostics;
using System.IO;
using System.Linq;
class P {
  static void Main(string[] args) {
    int n = args.Length>0?int.Parse(args[0]):200;
    string path = args.Length>1?args[1]:Path.Combine(Path.GetTempPath(),"fsync_probe_host.bin");
    var lat = new double[n];
    using (var fs = new FileStream(path, FileMode.Create, FileAccess.ReadWrite, FileShare.None, 4096, FileOptions.WriteThrough)) {
      var buf = new byte[4096];
      for (int i=0;i<buf.Length;i++) buf[i]=(byte)'x';
      fs.Write(buf,0,buf.Length); fs.Flush(true);
      var sw = new Stopwatch();
      for (int i=0;i<n;i++) {
        fs.Position=0; buf[0]=(byte)(i&255); fs.Write(buf,0,buf.Length);
        sw.Restart(); fs.Flush(true); sw.Stop();
        lat[i]=sw.Elapsed.TotalMilliseconds;
      }
    }
    try { File.Delete(path); } catch {}
    Array.Sort(lat);
    double pct(double p){ double k=(lat.Length-1)*(p/100.0); int lo=(int)Math.Floor(k); int hi=Math.Min(lo+1,lat.Length-1); double w=k-lo; return lat[lo]*(1-w)+lat[hi]*w; }
    Console.WriteLine($"{{\"path\":\"{path.Replace("\\","\\\\")}\",\"count\":{n},\"avg_ms\":{lat.Average():0.######},\"p50_ms\":{pct(50):0.######},\"p95_ms\":{pct(95):0.######},\"p99_ms\":{pct(99):0.######},\"min_ms\":{lat[0]:0.######},\"max_ms\":{lat[n-1]:0.######}}}");
  }
}
'@
$hostCs = Join-Path $out "HostFsync.cs"
Set-Content -Path $hostCs -Value $hostFsync -Encoding utf8
$hostExe = Join-Path $out "HostFsync.exe"
$hostJson = $null
try {
  # Use csc if available
  $csc = Get-Command csc -ErrorAction SilentlyContinue
  if (-not $csc) {
    $framework = @(Get-ChildItem "C:\Windows\Microsoft.NET\Framework64\v*\csc.exe" -ErrorAction SilentlyContinue | Sort-Object FullName -Descending)
    if ($framework.Count -gt 0) { $csc = $framework[0].FullName }
  } else { $csc = $csc.Source }
  if ($csc) {
    & $csc /nologo /out:$hostExe $hostCs | Out-Null
    $hostPath = Join-Path $out "fsync_probe_host.bin"
    $hostJson = & $hostExe $FsyncSamples $hostPath
    $hostJson | Set-Content -Encoding utf8 (Join-Path $out "raw_fsync_host.json")
    Write-Host "host fsync: $hostJson"
  } else {
    Write-Host "csc not found; skip host fsync"
  }
} catch {
  Write-Host "host fsync failed: $($_.Exception.Message)"
}

Write-Host "=== 2) single-thread COMMIT ===" -ForegroundColor Cyan
Mysql "UPDATE performance_schema.setup_consumers SET ENABLED='YES' WHERE NAME LIKE 'events_statements%' OR NAME LIKE 'events_waits%';" | Out-Null
Mysql "UPDATE performance_schema.setup_instruments SET ENABLED='YES', TIMED='YES' WHERE NAME LIKE 'wait/io/file/%' OR NAME LIKE 'statement/sql/%';" | Out-Null
# Reset digests
Mysql "TRUNCATE TABLE performance_schema.events_statements_summary_by_digest;" | Out-Null

$sw = [System.Diagnostics.Stopwatch]::StartNew()
Mysql "CALL fsync_probe.probe_commits($CommitSamples);" | Out-Null
$sw.Stop()
$singleWallAvg = $sw.Elapsed.TotalMilliseconds / $CommitSamples

$singleDigest = Mysql @"
SELECT DIGEST_TEXT, COUNT_STAR,
 ROUND(SUM_TIMER_WAIT/1e12,4), ROUND(AVG_TIMER_WAIT/1e9,3), ROUND(MAX_TIMER_WAIT/1e9,3)
FROM performance_schema.events_statements_summary_by_digest
WHERE DIGEST_TEXT IN ('COMMIT','START TRANSACTION') OR DIGEST_TEXT LIKE 'INSERT INTO ``t``%'
ORDER BY SUM_TIMER_WAIT DESC;
"@
$singleDigest | Set-Content -Encoding utf8 (Join-Path $out "single_commit_digest.tsv")

$hist = Mysql @"
SELECT ROUND(TIMER_WAIT/1e9, 3)
FROM performance_schema.events_statements_history_long
WHERE DIGEST_TEXT = 'COMMIT'
ORDER BY TIMER_START DESC
LIMIT $CommitSamples;
"@
$singleMs = @()
foreach ($line in @($hist)) { if ($line -match '^[0-9.]+$') { $singleMs += [double]$line } }
$singleSummary = SummarizeMs $singleMs
[pscustomobject]@{ wall_avg_ms = [Math]::Round($singleWallAvg,3); history = $singleSummary } |
  ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 (Join-Path $out "single_commit_summary.json")
Write-Host ("single wall_avg={0}ms history_p50={1}" -f ([Math]::Round($singleWallAvg,3)), $singleSummary.p50_ms)

Write-Host "=== 3) concurrent COMMIT workers=$CommitConcurrency ${BusySeconds}s ===" -ForegroundColor Cyan
Mysql "TRUNCATE TABLE performance_schema.events_statements_summary_by_digest;" | Out-Null
Mysql "TRUNCATE TABLE performance_schema.events_waits_summary_global_by_event_name;" | Out-Null
$before = Parse-StatusMap (Mysql "SHOW GLOBAL STATUS WHERE Variable_name IN ('Innodb_os_log_fsyncs','Innodb_data_fsyncs','Innodb_log_waits','Com_commit','Com_begin');")
$t0 = Get-Date
$jobs = @()
for ($w = 0; $w -lt $CommitConcurrency; $w++) {
  $jobs += Start-Job -ScriptBlock {
    param($Container, $Seconds)
    $deadline = (Get-Date).AddSeconds($Seconds)
    $n = 0
    while ((Get-Date) -lt $deadline) {
      docker exec -e MYSQL_PWD=fuchang-it-root $Container mysql -uroot -N -B -e "CALL fsync_probe.probe_commits(20);" 2>$null | Out-Null
      $n += 20
    }
    return $n
  } -ArgumentList $MysqlContainer, $BusySeconds
}
$null = $jobs | Wait-Job
$jobCommits = @($jobs | Receive-Job | Measure-Object -Sum).Sum
$jobs | Remove-Job -Force
$elapsed = ((Get-Date) - $t0).TotalSeconds
$after = Parse-StatusMap (Mysql "SHOW GLOBAL STATUS WHERE Variable_name IN ('Innodb_os_log_fsyncs','Innodb_data_fsyncs','Innodb_log_waits','Com_commit','Com_begin');")

$waits = Mysql @"
SELECT EVENT_NAME, COUNT_STAR,
 ROUND(SUM_TIMER_WAIT/1e12,4), ROUND(AVG_TIMER_WAIT/1e9,3), ROUND(MAX_TIMER_WAIT/1e9,3)
FROM performance_schema.events_waits_summary_global_by_event_name
WHERE COUNT_STAR > 0 AND (
  EVENT_NAME IN (
    'wait/io/file/innodb/innodb_log_file',
    'wait/io/file/sql/binlog',
    'wait/io/file/innodb/innodb_data_file',
    'wait/io/file/innodb/innodb_dblwr_file'
  )
  OR EVENT_NAME LIKE 'wait/synch/%binlog%'
  OR EVENT_NAME LIKE 'wait/synch/%log_system%'
  OR EVENT_NAME LIKE 'wait/synch/cond/innodb/%'
  OR EVENT_NAME LIKE 'wait/synch/mutex/innodb/log%'
)
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 30;
"@
$waits | Set-Content -Encoding utf8 (Join-Path $out "busy_waits.tsv")

$digestBusy = Mysql @"
SELECT DIGEST_TEXT, COUNT_STAR,
 ROUND(SUM_TIMER_WAIT/1e12,4), ROUND(AVG_TIMER_WAIT/1e9,3), ROUND(MAX_TIMER_WAIT/1e9,3)
FROM performance_schema.events_statements_summary_by_digest
WHERE DIGEST_TEXT IN ('COMMIT','START TRANSACTION')
ORDER BY SUM_TIMER_WAIT DESC;
"@
$digestBusy | Set-Content -Encoding utf8 (Join-Path $out "busy_commit_digest.tsv")

$busyHist = Mysql @"
SELECT ROUND(TIMER_WAIT/1e9, 3)
FROM performance_schema.events_statements_history_long
WHERE DIGEST_TEXT = 'COMMIT'
ORDER BY TIMER_START DESC
LIMIT 2000;
"@
$busyMs = @()
foreach ($line in @($busyHist)) { if ($line -match '^[0-9.]+$') { $busyMs += [double]$line } }
$busySummary = SummarizeMs $busyMs

$dCommit = $after['Com_commit'] - $before['Com_commit']
$dLogFs = $after['Innodb_os_log_fsyncs'] - $before['Innodb_os_log_fsyncs']
$dDataFs = $after['Innodb_data_fsyncs'] - $before['Innodb_data_fsyncs']

$rawObj = $null
try { $rawObj = $rawFsyncJson | ConvertFrom-Json } catch { $rawObj = @{ raw = $rawFsyncJson } }
$hostObj = $null
if ($hostJson) { try { $hostObj = $hostJson | ConvertFrom-Json } catch {} }

$verdict = "unknown"
if ($rawObj -and $rawObj.p50_ms -ne $null) {
  if ($rawObj.p50_ms -ge 15) {
    $verdict = "disk_or_volume_fsync_dominates"
  } elseif ($busySummary.p50_ms -gt ($rawObj.p50_ms * 2) -and ($dLogFs / [Math]::Max($dCommit,1)) -lt 0.7) {
    $verdict = "mysql_group_commit_or_dual_flush_scheduling"
  } elseif ($busySummary.p50_ms -gt ($rawObj.p50_ms * 1.5)) {
    $verdict = "mixed_disk_plus_mysql_scheduling"
  } else {
    $verdict = "raw_fsync_explains_commit"
  }
}

$result = [ordered]@{
  durability_unchanged = @{
    innodb_flush_log_at_trx_commit = 1
    sync_binlog = 1
  }
  raw_fsync_mysql_datadir_ms = $rawObj
  raw_fsync_host_ms = $hostObj
  single_thread_commit_ms = @{
    wall_avg_ms = [Math]::Round($singleWallAvg, 3)
    history = $singleSummary
  }
  concurrent_commit = @{
    workers = $CommitConcurrency
    seconds = [Math]::Round($elapsed, 3)
    job_commits = $jobCommits
    commits_delta = $dCommit
    commits_per_sec = [Math]::Round($dCommit / $elapsed, 2)
    innodb_os_log_fsyncs_delta = $dLogFs
    innodb_os_log_fsyncs_per_sec = [Math]::Round($dLogFs / $elapsed, 2)
    innodb_data_fsyncs_delta = $dDataFs
    innodb_data_fsyncs_per_sec = [Math]::Round($dDataFs / $elapsed, 2)
    log_fsyncs_per_commit = [Math]::Round($dLogFs / [Math]::Max($dCommit,1), 3)
    history = $busySummary
  }
  busy_waits_tsv = "busy_waits.tsv"
  verdict = $verdict
}
$result | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $out "fsync_commit_verdict.json")
$result | ConvertTo-Json -Depth 8
Write-Host "Wrote $out" -ForegroundColor DarkGreen
