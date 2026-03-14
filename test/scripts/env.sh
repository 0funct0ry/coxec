#!/bin/bash

# Color codes
RED='\033[0;31m'
CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
RESET='\033[0m'

echo -e "${CYAN}Execution Index:${RESET} ${GREEN}$COXEC_INDEX${RESET}"
echo -e "${CYAN}Timestamp:${RESET} ${GREEN}$(date +%s)${RESET}"
echo -e "${CYAN}PID:${RESET} ${GREEN}$$${RESET}"
echo -e "${CYAN}PWD:${RESET} ${YELLOW}$PWD${RESET}"
echo -e "${CYAN}RANDOM:${RESET} ${GREEN}$RANDOM${RESET}"
echo -e "${CYAN}USER:${RESET} ${BLUE}$USER${RESET}"
echo -e "${CYAN}HOME:${RESET} ${YELLOW}$HOME${RESET}"
echo -e "${CYAN}SHELL:${RESET} ${YELLOW}$SHELL${RESET}"
echo -e "${CYAN}HOSTNAME:${RESET} ${MAGENTA}$HOSTNAME${RESET}"
echo -e "${CYAN}LANG:${RESET} ${GREEN}$LANG${RESET}"
echo -e "${GREEN}Basic environment test completed.${RESET}"
echo -e "${RED}----------------------${RESET}"