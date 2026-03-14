#!/bin/bash
echo "Workflow Step $COXEC_INDEX: Initialization"
sleep 0.2

# Step 1: Validation
echo "  - Validating input for step $COXEC_INDEX"
validation_result=$((RANDOM % 100))
if [ $validation_result -lt 95 ]; then
    echo "  - Validation passed with score $validation_result"
else
    echo "  - Validation failed with score $validation_result"
    exit 1
fi

# Step 2: Processing
echo "  - Processing data for step $COXEC_INDEX"
processing_time=$((RANDOM % 500))
echo "  - Processed $processing_time records"

# Step 3: Finalization
echo "  - Finalizing workflow step $COXEC_INDEX"
checksum=$((COXEC_INDEX * 31 + validation_result))
echo "  - Checksum: $checksum"
echo "Workflow step $COXEC_INDEX completed successfully"
echo "----------------------"