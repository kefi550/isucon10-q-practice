#!/bin/bash
set -xe
set -o pipefail

# estate は isu1 の MySQL、chair は isu2 の MySQL に分かれている。
# それぞれ MYSQL_* / CHAIR_MYSQL_* (env.sh 参照) で接続先を切り替える。

CURRENT_DIR=$(cd $(dirname $0);pwd)
export LANG="C.UTF-8"
cd $CURRENT_DIR

MYSQL_HOST=${MYSQL_HOST:-127.0.0.1}
MYSQL_PORT=${MYSQL_PORT:-3306}
MYSQL_USER=${MYSQL_USER:-isucon}
MYSQL_DBNAME=${MYSQL_DBNAME:-isuumo}
export MYSQL_PWD=${MYSQL_PASS:-isucon}
cat 0_Schema_Estate.sql 1_DummyEstateData.sql | mysql --defaults-file=/dev/null -h "$MYSQL_HOST" -P "$MYSQL_PORT" -u "$MYSQL_USER" "$MYSQL_DBNAME"

CHAIR_MYSQL_HOST=${CHAIR_MYSQL_HOST:-127.0.0.1}
CHAIR_MYSQL_PORT=${CHAIR_MYSQL_PORT:-3306}
CHAIR_MYSQL_USER=${CHAIR_MYSQL_USER:-isucon}
CHAIR_MYSQL_DBNAME=${CHAIR_MYSQL_DBNAME:-isuumo}
export MYSQL_PWD=${CHAIR_MYSQL_PASS:-isucon}
cat 0_Schema_Chair.sql 2_DummyChairData.sql | mysql --defaults-file=/dev/null -h "$CHAIR_MYSQL_HOST" -P "$CHAIR_MYSQL_PORT" -u "$CHAIR_MYSQL_USER" "$CHAIR_MYSQL_DBNAME"
