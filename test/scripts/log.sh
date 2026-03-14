#!/bin/bash
level="INFO"
if [ $((COXEC_INDEX % 10)) -eq 0 ]; then
    level="ERROR"
elif [ $((COXEC_INDEX % 7)) -eq 0 ]; then
    level="WARN"
fi

message="Log entry for execution $COXEC_INDEX - System operational at $(date)"
echo "[$level] $message" 
echo "Additional debug info: UID=$UID, GID=$GID, Hostname=$(hostname)"

if [ "$level" = "ERROR" ]; then
    echo "ERROR DETAILS: This is a simulated error for testing purposes"
    exit 1
fi