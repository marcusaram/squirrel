#!/bin/bash
# Sends a zip to Apple's notary service and waits for the verdict.
#   scripts/notarize.sh FILE.zip CREDENTIALS...
# CREDENTIALS are notarytool's: --keychain-profile NAME, or
# --key KEY.p8 --key-id ID --issuer UUID. On a rejection it prints Apple's
# log, which says what was wrong, and fails.
set -euo pipefail
file=$1
shift
out=$(xcrun notarytool submit "$file" "$@" --wait --timeout 30m --output-format json)
echo "$out"
status=$(plutil -extract status raw - <<< "$out" 2> /dev/null || true)
if [ "$status" != Accepted ]; then
	id=$(plutil -extract id raw - <<< "$out" 2> /dev/null || true)
	[ -n "$id" ] && xcrun notarytool log "$id" "$@" || true
	echo "notarization of $file: ${status:-no answer}" >&2
	exit 1
fi
