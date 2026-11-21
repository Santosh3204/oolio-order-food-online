#!/usr/bin/env bash
# Writes three tiny gzip coupon files into data/sample so the whole flow can be
# tried in seconds instead of downloading the ~2 GB challenge files.
#   valid   (>= 2 files, 8-10 chars): HAPPYHRS FIFTYOFF BIRTHDAY
#   invalid (1 file only)           : SUPER100 ONLYONE1
#   invalid (too short, in all)     : SHORT
set -euo pipefail
dir="${1:-data/sample}"
mkdir -p "$dir"
printf 'HAPPYHRS\nBIRTHDAY\nSUPER100\nSHORT\nFILLER001\n' | gzip > "$dir/couponbase1.gz"
printf 'HAPPYHRS\nFIFTYOFF\nBIRTHDAY\nSHORT\nFILLER002\n'  | gzip > "$dir/couponbase2.gz"
printf 'FIFTYOFF\nBIRTHDAY\nONLYONE1\nSHORT\nFILLER003\n'  | gzip > "$dir/couponbase3.gz"
echo "sample coupon files written to $dir"
