#!/usr/bin/env bash
# Mock API Gateway Server for CodeTTY Demo (Clean / De-identified)
export TERM=xterm-256color
clear

printf "\033[1;34m[NEXUS-GATEWAY]\033[0m Initializing services...\n"
printf "\033[1;32m[READY]\033[0m HTTP server listening on \033[1;36mhttp://127.0.0.1:8080\033[0m\n"
printf "\033[1;32m[READY]\033[0m Loaded 24 routes across 4 service modules\n"
printf "\033[90m------------------------------------------------------------------------\033[0m\n"

endpoints=("/api/v1/auth/verify" "/api/v1/sessions" "/api/v1/health" "/api/v1/workers/status" "/api/v1/metrics")
methods=("GET" "POST" "GET" "GET" "GET")

# Initial burst of logs
printf "\033[90m2026-09-07T10:15:00Z\033[0m \033[1;32m200 OK \033[0m \033[33m 1.1ms\033[0m 127.0.0.1 \033[1mGET \033[0m /api/v1/health\n"
printf "\033[90m2026-09-07T10:15:01Z\033[0m \033[1;32m200 OK \033[0m \033[33m 3.4ms\033[0m 127.0.0.1 \033[1mPOST\033[0m /api/v1/auth/login\n"
printf "\033[90m2026-09-07T10:15:02Z\033[0m \033[1;32m200 OK \033[0m \033[33m 0.8ms\033[0m 127.0.0.1 \033[1mGET \033[0m /api/v1/health\n"
printf "\033[90m2026-09-07T10:15:04Z\033[0m \033[1;32m200 OK \033[0m \033[33m 5.2ms\033[0m 127.0.0.1 \033[1mGET \033[0m /api/v1/sessions\n"
printf "\033[90m2026-09-07T10:15:06Z\033[0m \033[1;32m200 OK \033[0m \033[33m 2.0ms\033[0m 127.0.0.1 \033[1mGET \033[0m /api/v1/workers/status\n"

# Ongoing steady logs
while true; do
    idx=$(( RANDOM % 5 ))
    lat=$(( RANDOM % 15 + 1 ))
    now=$(date +"%Y-%m-%dT%H:%M:%SZ")
    printf "\033[90m%s\033[0m \033[1;32m200 OK \033[0m \033[33m%3d.%1dms\033[0m 127.0.0.1 \033[1m%-4s\033[0m %s\n" \
        "$now" "$lat" "$(( RANDOM % 10 ))" "${methods[$idx]}" "${endpoints[$idx]}"
    sleep $(( RANDOM % 3 + 2 ))
done
