#!/bin/bash
# Simulate an API call with variable response time
delay=$((RANDOM % 3))
echo "API Call $COXEC_INDEX: Request sent at $(date)"
sleep $delay
status_code=$((200 + RANDOM % 3))  # 200, 201, or 202
echo "API Call $COXEC_INDEX: Response $status_code after ${delay}s"
echo "Response body: {\"id\": $COXEC_INDEX, \"status\": \"success\", \"delay\": $delay}"