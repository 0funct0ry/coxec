#!/bin/bash
# Simulate a small data processing pipeline
echo "Starting data process for batch $COXEC_INDEX" >  "/tmp/coxec_batch_$COXEC_INDEX.log"
echo "Step 1: Extracting data..." >> "/tmp/coxec_batch_$COXEC_INDEX.log"
sleep 0.1
echo "Step 2: Transforming data..." >> "/tmp/coxec_batch_$COXEC_INDEX.log"
sleep 0.1
echo "Step 3: Loading data..." >> "/tmp/coxec_batch_$COXEC_INDEX.log"
echo "Process $COXEC_INDEX completed at $(date)" >> "/tmp/coxec_batch_$COXEC_INDEX.log"
cat "/tmp/coxec_batch_$COXEC_INDEX.log"
rm "/tmp/coxec_batch_$COXEC_INDEX.log"
echo "------------------------"