#!/bin/bash

set -euo pipefail

OCI_IMAGE_NAME=$1

# Start acceld service
sudo nohup ./acceld --config ./misc/config/config.nydus.yaml &> acceld.log &
sleep 1

# Convert image by accelctl
sudo ./accelctl task create --sync localhost/library/$OCI_IMAGE_NAME:latest

# Verify filesystem consistency for converted image
sudo /usr/bin/nydusify check \
  --nydus-image /usr/bin/nydus-image \
  --nydusd /usr/bin/nydusd \
  --source localhost/library/$OCI_IMAGE_NAME:latest \
  --target localhost/library/$OCI_IMAGE_NAME:latest-nydus \
  --backend-type registry \
  --backend-config "{\"scheme\":\"http\",\"host\":\"localhost\",\"repo\":\"library/$OCI_IMAGE_NAME\",\"auth\":\"YWRtaW46SGFyYm9yMTIzNDU=\"}"

# Gracefully exit acceld
sudo pkill -SIGINT acceld
