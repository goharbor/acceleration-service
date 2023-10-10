#!/bin/bash

set -euo pipefail

OCI_IMAGE_NAME=$1

# Convert image by accelctl in one-time mode
sudo ./accelctl convert --config ./misc/config/config.nydus.yaml localhost/library/$OCI_IMAGE_NAME:latest

# Verify filesystem consistency for converted image
sudo /usr/bin/nydusify check \
  --source localhost/library/$OCI_IMAGE_NAME:latest \
  --target localhost/library/$OCI_IMAGE_NAME:latest-nydus
