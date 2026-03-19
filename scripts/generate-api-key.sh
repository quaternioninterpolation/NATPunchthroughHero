#!/usr/bin/env bash
# Generate a random API key and save it to output/api_key.txt.
set -euo pipefail

KEY=$(openssl rand -hex 16 2>/dev/null || head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')

mkdir -p output
echo "$KEY" > output/api_key.txt

echo "API key: $KEY"
echo "Saved to output/api_key.txt"
