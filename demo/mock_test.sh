#!/usr/bin/env bash
# Mock Test Runner for CodeTTY Demo (Clean / De-identified)
export TERM=xterm-256color
clear

printf "\033[1mNEXUS TEST SUITE v2.4.0\033[0m\n"
printf "\033[90mWatching files across ./src, ./pkg for changes...\033[0m\n\n"

printf "\033[1;32m PASS \033[0m \033[36mpkg/auth/token_test.go\033[0m (18 tests) \033[90m14ms\033[0m\n"
printf "\033[1;32m PASS \033[0m \033[36mpkg/router/api_test.go\033[0m (26 tests) \033[90m22ms\033[0m\n"
printf "\033[1;32m PASS \033[0m \033[36mpkg/worker/pool_test.go\033[0m (8 tests) \033[90m41ms\033[0m\n"
printf "\033[1;32m PASS \033[0m \033[36msrc/gateway/proxy_test.go\033[0m (12 tests) \033[90m19ms\033[0m\n\n"

printf "\033[1mTest Suites:\033[0m \033[1;32m4 passed\033[0m, 4 total\n"
printf "\033[1mTests:\033[0m       \033[1;32m64 passed\033[0m, 64 total\n"
printf "\033[1mSnapshots:\033[0m   0 total\n"
printf "\033[1mTime:\033[0m        1.342 s\n"
printf "\033[90mRan all test suites.\033[0m\n\n"
printf "\033[7;32m WATCH \033[0m \033[90mWaiting for file changes... (press q to quit, a to run all)\033[0m\n"

while true; do
    sleep 3600
done
