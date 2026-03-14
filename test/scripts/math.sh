#!/bin/bash
index=$COXEC_INDEX
result=$((index * index + 10))
echo "Calculation for run $index: ($index^2 + 10) = $result"
if [ $((index % 2)) -eq 0 ]; then
    echo "  -> Run number $index is even"
else
    echo "  -> Run number $index is odd"
fi