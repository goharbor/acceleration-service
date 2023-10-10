#!/bin/bash

set -euo pipefail

# Start acceld service
sudo nohup ./acceld --config ./script/integration/webhook/config.yaml &> acceld.log &
sleep 1

IP=$(ip addr show eth0 | grep -oP 'inet \K[\d.]+')

# create webhook
while :
do
response_code=$(curl -b "" --location 'http://0.0.0.0/api/v2.0/projects/library/webhook/policies' \
    --header 'accept: application/json' \
    --header 'X-Request-Id: 1' \
    --header 'X-Is-Resource-Name: false' \
    --header 'authorization: Basic YWRtaW46SGFyYm9yMTIzNDU=' \
    --header 'Content-Type: application/json' \
    --data "{
      \"id\": 0,
      \"name\": \"Acceleration Service\",
      \"description\": \"test for acceleration service\",
      \"project_id\": 0,
      \"targets\": [
        {
          \"type\": \"http\",
          \"address\": \"http://${IP}:2077/api/v1/conversions\",
          \"auth_header\": \"acceleration-service\",
          \"skip_cert_verify\": true
        }
      ],
      \"event_types\": [
        \"PUSH_ARTIFACT\"
      ],
      \"creator\": \"admin\",
      \"creation_time\": \"2023-09-19T03:54:32.967Z\",
      \"update_time\": \"2023-09-19T03:54:32.967Z\",
      \"enabled\": true
    }" -w "%{http_code}\n" -o /dev/null)

    if [ "$response_code" -ge 200 ] && [ "$response_code" -lt 300 ]; then
      break
    fi

    sleep 5
done

docker pull alpine
docker login -u admin -p Harbor12345 localhost
docker tag alpine localhost/library/alpine
docker push localhost/library/alpine

sleep 5

count=0
while [ $count -lt 60 ]; do
  response=$(curl -b "" --location 'http://0.0.0.0/api/v2.0/projects/library/repositories/alpine/artifacts/latest-nydus/tags?page=1&page_size=10&with_signature=false&with_immutable_status=false' \
  --header 'accept: application/json' \
  --header 'X-Request-Id: 1' \
  -s -o /dev/null -w "%{http_code}")

  cat acceld.log
  if [ "$response" == "200" ]; then
    curl -b "" --location 'http://0.0.0.0/api/v2.0/projects/library/repositories/alpine/artifacts/latest-nydus/tags?page=1&page_size=10&with_signature=false&with_immutable_status=false' --header 'accept: application/json'
    if grep -E 'level=error|panic:' acceld.log; then
      exit 1
    fi
    exit 0
  fi

  sleep 1
  count=$((count + 1))
done

curl -b "" --location 'http://0.0.0.0api/v2.0/projects/library/repositories/alpine/artifacts/latest-nydus/tags?page=1&page_size=10&with_signature=false&with_immutable_status=false' --header 'accept: application/json'
exit 1
