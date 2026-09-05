#!/bin/bash

set -eu

LOCAL_PATH="."

mkdir -p ${LOCAL_PATH}/webapp/go/
rsync -av isu1:~/isuumo/webapp/go/ "${LOCAL_PATH}/webapp/go/"

mkdir -p ${LOCAL_PATH}/webapp/mysql/
rsync -avr isu1:~/isuumo/webapp/mysql/ "${LOCAL_PATH}/webapp/mysql/"

# envの代表としてisu1だけとる
rsync -avr isu1:~/env.sh "${LOCAL_PATH}/env.sh"

for i in $(seq 1 1); do
  host="isu${i}"
  mkdir -p ${LOCAL_PATH}/${host}/etc/systemd/system/
  rsync -avr ${host}:/etc/hosts "${LOCAL_PATH}/${host}/etc/"
  rsync -avr ${host}:/etc/systemd/system/isuumo.go.service "${LOCAL_PATH}/${host}/etc/systemd/system/"
  mkdir -p ${LOCAL_PATH}/${host}/etc/mysql/mysql.conf.d/
  rsync -avr ${host}:/etc/mysql/mysql.conf.d/ "${LOCAL_PATH}/${host}/etc/mysql/mysql.conf.d"
  mkdir -p ${LOCAL_PATH}/${host}/etc/nginx/sites-available
  rsync -avr ${host}:/etc/nginx/sites-available "${LOCAL_PATH}/${host}/etc/nginx/sites-available"
  mkdir -p ${LOCAL_PATH}/${host}/etc/logrotate.d/
  rsync -avr ${host}:/etc/logrotate.d/ "${LOCAL_PATH}/${host}/etc/logrotate.d/"
done
