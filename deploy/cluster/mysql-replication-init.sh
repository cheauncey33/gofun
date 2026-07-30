#!/bin/sh
set -eu

until MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqladmin ping -h mysql-primary -uroot --silent; do
  sleep 2
done
until MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqladmin ping -h mysql-replica -uroot --silent; do
  sleep 2
done

MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -h mysql-primary -uroot -e "
CREATE USER IF NOT EXISTS 'replicator'@'%' IDENTIFIED BY '${MYSQL_REPLICATION_PASSWORD}';
GRANT REPLICATION SLAVE ON *.* TO 'replicator'@'%';
FLUSH PRIVILEGES;
"

MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -h mysql-replica -uroot -e "
STOP REPLICA;
RESET REPLICA ALL;
CHANGE REPLICATION SOURCE TO
  SOURCE_HOST='mysql-primary',
  SOURCE_PORT=3306,
  SOURCE_USER='replicator',
  SOURCE_PASSWORD='${MYSQL_REPLICATION_PASSWORD}',
  SOURCE_AUTO_POSITION=1,
  GET_SOURCE_PUBLIC_KEY=1;
START REPLICA;
SET PERSIST read_only=ON;
SET PERSIST super_read_only=ON;
"

for attempt in $(seq 1 60); do
  status="$(MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -h mysql-replica -uroot -N -B -e "
    SELECT CONCAT(
      COALESCE(MAX(CASE WHEN CHANNEL_NAME='' THEN SERVICE_STATE END), 'OFF'),
      '/',
      COALESCE(MAX(CASE WHEN CHANNEL_NAME='' THEN LAST_ERROR_NUMBER END), -1)
    )
    FROM performance_schema.replication_applier_status_by_coordinator;
  " 2>/dev/null || true)"
  if [ "$status" = "ON/0" ]; then
    exit 0
  fi
  sleep 2
done

MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -h mysql-replica -uroot -e "SHOW REPLICA STATUS\G"
exit 1
