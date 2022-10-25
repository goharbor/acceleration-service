#!/bin/bash

set -euo pipefail

OCI_IMAGE_NAME=$1

# Start acceld service
sudo nohup ./acceld --config ./misc/config/config.estargz.yaml &> acceld.log &
sleep 1

# Convert image by accelctl
sudo ./accelctl task create --sync localhost/library/$OCI_IMAGE_NAME:latest

# Verify filesystem consistency for converted image
# TODO

# Gracefully exit acceld
sudo pkill -SIGINT acceld
