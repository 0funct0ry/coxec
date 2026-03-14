#!/bin/bash
echo "=== Resource Check for Execution $COXEC_INDEX ==="
echo "CPU Info:"
grep 'model name' /proc/cpuinfo | head -n1 | awk -F': ' '{print $2}'
echo "Memory Usage:"
free -h | grep '^Mem:' | awk '{print "Total: " $2 ", Used: " $3 ", Free: " $4}'
echo "Disk Usage (root):"
df -h / | tail -n1 | awk '{print "Used: " $5 " of " $2}'
echo "Current Processes: $(ps -e --no-headers | wc -l)"
echo "Execution $COXEC_INDEX resource check completed."
echo "----------------------"