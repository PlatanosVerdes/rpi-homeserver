#!/bin/bash
# TEMPORARY SHIM, added 2026-08-22 with the move of scripts/ into folders. Delete after the
# first successful deploy that follows that merge (see PENDING.md).
#
# Without it the move is a deadlock, not a rename. The live crontab calls this path; the pull
# that lands the move deletes it; cron then fails every 30 minutes and never reaches the new
# install-crontab.sh that would point it at the new path. Nothing self-heals, and the only
# symptom is metrics quietly going stale.
#
# One deploy through this shim is enough: the new apply.sh reinstalls the crontab with the new
# paths, and nothing calls this file again.
exec "$(dirname "${BASH_SOURCE[0]}")/deploy/apply.sh" "$@"
