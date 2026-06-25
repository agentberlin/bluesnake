#!/usr/bin/env bash
#
# Resilient Apple notarization for a single artifact (a .zip or .dmg).
#
# Why this exists: `xcrun notarytool submit --wait` uploads the artifact and then
# polls Apple for the verdict in one shot — but a SINGLE transient network blip
# during that poll (e.g. NSURLErrorNotConnectedToInternet / -1009 on the runner)
# makes it exit non-zero and fail the whole release, even though nothing is
# actually wrong. The window is widest on a brand-new Developer ID account, whose
# first submission can sit "In Progress" for 30+ minutes. We hit exactly that.
#
# This helper instead:
#   1. Uploads the artifact ONCE (`submit`, no --wait) and captures the id.
#   2. Polls with `notarytool wait` in a retry loop that tolerates transient
#      network errors — each retry re-attaches to the SAME submission, so it
#      never re-uploads or restarts the clock.
#   3. Asserts the final status is "Accepted" via `notarytool info` (an exit code
#      alone is not trusted — `wait`/`submit --wait` have historically returned 0
#      on non-Accepted terminal states). On any non-Accepted status it dumps the
#      full notarization log and fails.
#
# The caller still runs `xcrun stapler staple` afterward as a final backstop.
#
# Usage:
#   APPLE_ID=… APPLE_PASSWORD=… APPLE_TEAM_ID=… packaging/notarize-macos.sh <artifact>
#
set -euo pipefail

artifact="${1:?usage: notarize-macos.sh <path-to-.zip-or-.dmg>}"
: "${APPLE_ID:?APPLE_ID is required}"
: "${APPLE_PASSWORD:?APPLE_PASSWORD is required}"
: "${APPLE_TEAM_ID:?APPLE_TEAM_ID is required}"

auth=(--apple-id "$APPLE_ID" --password "$APPLE_PASSWORD" --team-id "$APPLE_TEAM_ID")

status_of() {
  # Print the submission status, or empty string on any error (caller decides).
  xcrun notarytool info "$1" "${auth[@]}" --output-format json 2>/dev/null \
    | jq -r '.status // empty' 2>/dev/null || true
}

echo "Submitting $artifact for notarization…"
submission_id="$(xcrun notarytool submit "$artifact" "${auth[@]}" --output-format json | jq -r '.id')"
if [ -z "$submission_id" ] || [ "$submission_id" = "null" ]; then
  echo "::error::notarytool submit did not return a submission id"
  exit 1
fi
echo "Submission id: $submission_id"

attempts=30   # ~ up to 30 transient retries, 30s apart, on top of notarytool's own wait
for i in $(seq 1 "$attempts"); do
  if xcrun notarytool wait "$submission_id" "${auth[@]}"; then
    break   # reached a terminal state cleanly
  fi
  # `wait` exited non-zero: distinguish a transient poll error from a real verdict.
  st="$(status_of "$submission_id")"
  case "$st" in
    Accepted|Invalid|Rejected)
      echo "Submission reached terminal status during retry: $st"
      break
      ;;
  esac
  if [ "$i" -ge "$attempts" ]; then
    echo "::error::notarytool wait kept failing after $attempts attempts (status=${st:-unknown})"
    break
  fi
  echo "::warning::notarytool wait attempt $i failed (transient network error?; status=${st:-unknown}); retrying in 30s"
  sleep 30
done

status="$(status_of "$submission_id")"
echo "Notarization status: ${status:-unknown}"
if [ "$status" != "Accepted" ]; then
  echo "::error::notarization not Accepted (status=${status:-unknown}) for $artifact — full log follows:"
  xcrun notarytool log "$submission_id" "${auth[@]}" || true
  exit 1
fi
echo "Notarization Accepted: $artifact"
