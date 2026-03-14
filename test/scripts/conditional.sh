#!/bin/bash
echo "Running execution $COXEC_INDEX"
if [ $((COXEC_INDEX % 3)) -eq 0 ]; then
    echo "Intentionally failing on run $COXEC_INDEX"
    exit 1
else
    echo "Success for run $COXEC_INDEX"
    echo "Random value: $RANDOM"
fi
echo "-------------------------"