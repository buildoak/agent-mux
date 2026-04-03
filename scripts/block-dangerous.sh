#!/usr/bin/env bash
set -euo pipefail
if [[ "${HOOK_COMMAND:-}" =~ git\ push\ --force ]] || [[ "${HOOK_COMMAND:-}" =~ git\ push\ -f\  ]] ; then
    echo "Blocked: force push detected" >&2; exit 1
fi
if [[ "${HOOK_COMMAND:-}" =~ rm\ -rf\ / ]] || [[ "${HOOK_COMMAND:-}" =~ rm\ -rf\ \~ ]]; then
    echo "Blocked: destructive rm -rf on critical path" >&2; exit 1
fi
if [[ "${HOOK_COMMAND:-}" =~ DROP\ TABLE ]] || [[ "${HOOK_COMMAND:-}" =~ drop\ table ]]; then
    echo "Blocked: DROP TABLE detected" >&2; exit 1
fi
if [[ "${HOOK_FILE_PATH:-}" =~ ^/etc/ ]] || [[ "${HOOK_FILE_PATH:-}" =~ ^/System/ ]]; then
    echo "Blocked: write to system path ${HOOK_FILE_PATH}" >&2; exit 1
fi
if [[ "${HOOK_PHASE:-}" == "pre_dispatch" ]]; then
    for text in "${HOOK_PROMPT:-}" "${HOOK_SYSTEM_PROMPT:-}"; do
        if [[ "$text" =~ rm\ -rf\ / ]] || [[ "$text" =~ DROP\ TABLE ]] || [[ "$text" =~ drop\ table ]]; then
            echo "Blocked: dangerous pattern in prompt" >&2; exit 1
        fi
    done
fi
if [[ "${HOOK_COMMAND:-}" =~ curl\  ]] || [[ "${HOOK_COMMAND:-}" =~ wget\  ]]; then
    echo "Warning: network command detected" >&2; exit 2
fi
if [[ "${HOOK_FILE_PATH:-}" =~ \.env$ ]] || [[ "${HOOK_FILE_PATH:-}" =~ credentials ]] || [[ "${HOOK_FILE_PATH:-}" =~ \.pem$ ]]; then
    echo "Warning: access to secrets-adjacent file" >&2; exit 2
fi
exit 0
