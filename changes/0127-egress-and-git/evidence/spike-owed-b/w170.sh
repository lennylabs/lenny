#!/bin/bash
for i in $(seq 1 85); do if eval "$1"; then echo "met"; exit 0; fi; sleep 2; done; echo "not yet"
