#!/bin/bash
# Git credential helper — outputs GITHUB_TOKEN for whatever host git asks
# about. Registered per-host via GIT_CONFIG_* in agent.ts.
if [ "$1" = "get" ]; then
    if [ -n "$GITHUB_TOKEN" ]; then
        host=""
        while IFS= read -r line; do
            case "$line" in
                host=*) host="${line#host=}";;
            esac
        done
        echo "protocol=https"
        if [ -n "$host" ]; then
            echo "host=$host"
        fi
        echo "username=x-access-token"
        echo "password=$GITHUB_TOKEN"
    fi
fi
