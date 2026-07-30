#!/bin/sh
set -eu

until master_ip="$(getent hosts redis-primary | awk 'NR == 1 { print $1 }')" &&
  [ -n "$master_ip" ]; do
  sleep 1
done

cat > /tmp/sentinel.conf <<EOF
port 26379
bind 0.0.0.0
protected-mode no
sentinel monitor ${REDIS_MASTER_NAME} ${master_ip} 6379 2
sentinel auth-pass ${REDIS_MASTER_NAME} ${REDIS_PASSWORD}
sentinel down-after-milliseconds ${REDIS_MASTER_NAME} 5000
sentinel failover-timeout ${REDIS_MASTER_NAME} 15000
sentinel parallel-syncs ${REDIS_MASTER_NAME} 1
EOF

exec redis-server /tmp/sentinel.conf --sentinel
