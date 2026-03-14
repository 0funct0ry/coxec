#!/bin/bash
start_time=$(date +%s.%N)
if curl -s --max-time 5 https://httpbin.org/status/200; then
    end_time=$(date +%s.%N)
    duration=$(echo "$end_time - $start_time" | bc -l)
    echo "[SUCCESS] HTTP check for run $COXEC_INDEX took ${duration}s"
else
    echo "[FAILED] Network check failed for run $COXEC_INDEX"
fi