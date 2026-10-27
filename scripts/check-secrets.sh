#!/usr/bin/env bash
# Fails if a real-looking alert channel secret is committed: Telegram bot
# tokens, Slack webhook URLs, Discord webhooks. Keep them in Settings or in
# ~/.micelio/micelio.env instead (see docs/USER-GUIDE.md).
set -euo pipefail
patterns=(
  '[0-9]{8,10}:AA[A-Za-z0-9_-]{30,}'                                  # Telegram bot token
  'hooks\.slack\.com/services/T[A-Z0-9]{6,}/B[A-Z0-9]{6,}/[A-Za-z0-9]{20,}' # Slack webhook
  'discord(app)?\.com/api/webhooks/[0-9]{15,}/[A-Za-z0-9_-]{30,}'      # Discord webhook
)
found=0
for p in "${patterns[@]}"; do
  if git grep -nIE "$p" -- . ':!cmd/micelio/dashboard/dist' ':!scripts/check-secrets.sh'; then
    found=1
  fi
done
if [ "$found" -ne 0 ]; then
  echo "Secret-looking values found above: move them to Settings or ~/.micelio/micelio.env." >&2
  exit 1
fi
echo "No committed channel secrets."
