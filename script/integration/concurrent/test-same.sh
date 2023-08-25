#!/bin/bash

set -euo pipefail

# Start acceld service
sudo nohup ./acceld --config ./script/integration/concurrent/config.yaml &> acceld.log &
sleep 1

# Convert image by accelctl
for ((i=1; i<=10; i++)); do
   ./accelctl task create localhost/library/$1:latest
done

while true; do
  # Check tasks list
  ./accelctl task list

  # Get tasks status
  status=$(./accelctl task list| tail -n +2 | awk '{print $4}')

  # Check any FAILED
  if echo "$status" | grep -q "FAILED"; then
    cat acceld.log
    echo "Found a task failed"
    exit 1
  fi

  # Check if all tasks completed
  if echo "$status" | grep -q "PROCESSING"; then
    sleep 10
  else
    break
  fi
done

# check acceld log
cat acceld.log

# Gracefully exit acceld
sudo pkill -SIGINT acceld
