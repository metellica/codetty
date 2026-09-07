#!/usr/bin/env bash
# Mock Top/Monitor for CodeTTY Demo (Clean / De-identified)
export TERM=xterm-256color

while true; do
    clear
    now=$(date +"%H:%M:%S")
    printf "\033[1;37;44m nexus-top - %s up 14 days,  1 user,  load average: 0.28, 0.35, 0.40 \033[0m\n" "$now"
    printf "Tasks: \033[1m28 total\033[0m,   \033[1;32m2 running\033[0m,  26 sleeping,   0 stopped,   0 zombie\n"
    printf "%%Cpu(s):  \033[1;32m 4.8\033[0m us,  \033[1;34m 1.6\033[0m sy,   0.0 ni,  \033[1m93.4\033[0m id,   0.2 wa,   0.0 hi,   0.0 si\n"
    printf "MiB Mem :  \033[1m16384.0\033[0m total,   \033[1;32m9420.5\033[0m free,   \033[1;33m3820.1\033[0m used,   3143.4 buff/cache\n"
    printf "MiB Swap:   \033[1m4096.0\033[0m total,   \033[1;32m4096.0\033[0m free,      \033[1;33m0.0\033[0m used.  12140.2 avail Mem\n\n"
    
    printf "\033[7m  PID USER      PR  NI    VIRT    RES    SHR S  %%CPU  %%MEM     TIME+ COMMAND             \033[0m\n"
    printf " 1042 dev       20   0  814200  92140  32100 S   5.8   0.6   0:24.18 \033[1;36mnexus-api\033[0m\n"
    printf " 1088 dev       20   0 1342100 148200  48100 S   4.2   0.9   0:38.45 \033[1;35mcopilot\033[0m\n"
    printf " 1102 dev       20   0 1984200 224100  64200 S   3.6   1.4   0:51.10 \033[1;38;5;208mclaude\033[0m\n"
    printf " 1150 dev       20   0  421000  45200  18200 S   1.2   0.3   0:11.02 \033[1;32mtest-runner\033[0m\n"
    printf " 1205 dev       20   0  182400  28100  12400 S   0.4   0.2   0:02.14 \033[90mworker-pool\033[0m\n"
    printf " 1240 dev       20   0  156200  24200  11200 S   0.2   0.1   0:01.40 \033[90mmetrics-collector\033[0m\n"
    
    sleep 2
done
