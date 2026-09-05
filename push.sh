#!/bin/bash

set -eu

LOCAL_PATH="."
NGINX_CONFIG="${LOCAL_PATH}/isu1/etc/nginx/sites-available/sites-available/isuumo.conf"

for i in $(seq 1); do
  host="isu${i}"
  echo $host
  rsync -avr "${LOCAL_PATH}/webapp/go/" ${host}:~/isuumo/webapp/go/
  rsync -avr "${LOCAL_PATH}/env.sh" ${host}:~/env.sh
  rsync -avr "${LOCAL_PATH}/webapp/mysql/db/" "${host}:~/isuumo/webapp/mysql/db/"
  rsync -av "${NGINX_CONFIG}" "${host}:/tmp/isuumo.conf.codex"
  ssh ${host} 'bash -l -c "sudo logrotate -f /etc/logrotate.conf; export PATH=/home/isucon/local/go/bin:/home/isucon/go/bin:\$PATH; cd ~/isuumo/webapp/go; rm isuumo; make; sudo systemctl restart isuumo.go.service;"'
  ssh ${host} 'sudo cp /etc/nginx/sites-available/isuumo.conf /tmp/isuumo.conf.previous && sudo cp /tmp/isuumo.conf.codex /etc/nginx/sites-available/isuumo.conf && if sudo nginx -t; then sudo systemctl reload nginx; else sudo cp /tmp/isuumo.conf.previous /etc/nginx/sites-available/isuumo.conf; exit 1; fi'
done
