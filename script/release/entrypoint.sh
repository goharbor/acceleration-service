#!/bin/bash

set -eu

/usr/local/bin/containerd &
sleep 3
/usr/local/bin/acceld --config $1
