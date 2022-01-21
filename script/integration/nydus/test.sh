#!/bin/bash

set -euo pipefail

OCI_IMAGE_NAME=$1
NYDUS_VERSION=2.0.0-rc.0

# Download nydus components
wget https://github.com/dragonflyoss/image-service/releases/download/v$NYDUS_VERSION/nydus-static-v$NYDUS_VERSION-x86_64.tgz
tar xzvf nydus-static-v$NYDUS_VERSION-x86_64.tgz
mkdir -p /usr/bin
sudo mv nydus-static/nydus-image nydus-static/nydusd-fusedev nydus-static/nydusify /usr/bin/

# Start acceld service
sudo nohup ./acceld --config ./misc/config/config.yaml.nydus.tmpl &> acceld.log &
sleep 1

# Convert image by accelctl
sudo ./accelctl task create --sync localhost/library/$OCI_IMAGE_NAME:latest

# Verify filesystem consistency for converted image
sudo /usr/bin/nydusify check \
  --nydus-image /usr/bin/nydus-image \
  --nydusd /usr/bin/nydusd-fusedev \
  --source localhost/library/$OCI_IMAGE_NAME:latest \
  --target localhost/library/$OCI_IMAGE_NAME:latest-nydus \
  --backend-type registry \
  --backend-config "{\"scheme\":\"http\",\"host\":\"localhost\",\"repo\":\"library/$OCI_IMAGE_NAME\",\"auth\":\"YWRtaW46SGFyYm9yMTIzNDU=\"}"

# Gracefully exit acceld
sudo pkill -SIGINT acceld
