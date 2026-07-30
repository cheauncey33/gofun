#!/bin/sh
set -eu
rm -f /tmp/cluster-ready

docker-entrypoint.sh rabbitmq-server &
server_pid=$!

until su-exec rabbitmq rabbitmq-diagnostics -q ping; do
  sleep 2
done

if [ -n "${RABBITMQ_CLUSTER_SEED:-}" ]; then
  su-exec rabbitmq rabbitmqctl stop_app
  su-exec rabbitmq rabbitmqctl reset
  until su-exec rabbitmq rabbitmqctl join_cluster "rabbit@${RABBITMQ_CLUSTER_SEED}"; do
    sleep 2
  done
  su-exec rabbitmq rabbitmqctl start_app
fi

touch /tmp/cluster-ready
wait "$server_pid"
