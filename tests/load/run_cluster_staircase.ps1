param(
    [string]$Steps = "250,500,750,1000,1500",
    [int]$Runs = 3,
    [string]$RunLabel = "cluster-baseline-20260731"
)

$ErrorActionPreference = "Stop"
$env:BASE_URL = "http://127.0.0.1:8088/api/v1"
$env:METRICS_URL = "http://127.0.0.1:8088/metrics"
$env:STEPS = $Steps
$env:RUNS = [string]$Runs
$env:WAVE_SIZE = "50"
$env:POLL_SECONDS = "180"
$env:COOLDOWN_SECONDS = "3"
$env:SETUP_CONCURRENCY = "30"
$env:LOAD_USER_PREFIX = "cluster_"
$env:RUN_LABEL = $RunLabel
$env:RESULTS_DIR = "tests/load/results/$RunLabel"
$env:MONITOR_CONTAINERS = @(
    "whu-snack-go-cluster-frontend-1",
    "whu-snack-go-cluster-backend-1-1",
    "whu-snack-go-cluster-backend-2-1",
    "whu-snack-go-cluster-backend-3-1",
    "whu-snack-go-cluster-mysql-primary-1",
    "whu-snack-go-cluster-mysql-replica-1",
    "whu-snack-go-cluster-redis-primary-1",
    "whu-snack-go-cluster-redis-replica-1",
    "whu-snack-go-cluster-rabbitmq-1-1",
    "whu-snack-go-cluster-rabbitmq-2-1",
    "whu-snack-go-cluster-rabbitmq-3-1",
    "whu-snack-go-cluster-rabbitmq-lb-1"
) -join ","
$env:MYSQL_CONTAINER = "whu-snack-go-cluster-mysql-primary-1"
$env:REDIS_CONTAINER = "whu-snack-go-cluster-redis-primary-1"
$env:RABBITMQ_CONTAINER = "whu-snack-go-cluster-rabbitmq-1-1"
$env:ORDER_OUTBOX_WRITE_MODE = "batch"
$env:ORDER_CONSUMER_WORKER_COUNT = "2x3"
$env:ORDER_CONSUMER_PREFETCH_COUNT = "5"
$env:ORDER_OUTBOX_PUBLISH_WORKERS = "2x3"
$env:ORDER_OUTBOX_PUBLISH_BATCH = "200"
$env:MYSQL_MAX_OPEN_CONNS = "50x3"
$env:REDIS_POOL_SIZE = "50x3"

node tests/load/ticket_rush_staircase.mjs
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}
