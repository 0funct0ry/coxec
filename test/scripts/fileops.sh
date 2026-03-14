#!/bin/bash
timestamp=$(date +%Y%m%d_%H%M%S)
filename="temp_file_${COXEC_INDEX}_${timestamp}.txt"
echo "Created by coxec run $COXEC_INDEX at $(date)" > "$filename"
ls -la "$filename"
rm "$filename"
echo "Temporary file operation complete for index $COXEC_INDEX"