#!/bin/bash

set -eu

LOCAL_PATH="."

for i in $(seq 1); do
  host="isu${i}"
  echo $host
  rsync -avr "${LOCAL_PATH}/webapp/go/" ${host}:~/isuumo/webapp/go/ 
  # rsync -avr "${LOCAL_PATH}/env.sh" ${host}:~/env.sh
  rsync -avr "${LOCAL_PATH}/webapp/mysql/db/" "${host}:~/isuumo/webapp/mysql/db/"
  ssh ${host} 'bash -l -c "sudo logrotate -f /etc/logrotate.conf; export PATH=/home/isucon/local/go/bin:/home/isucon/go/bin:\$PATH; cd ~/isuumo/webapp/go; rm isuumo; make; sudo systemctl restart isuumo.go.service;"'
done
