#!/bin/bash

# Terminate all running Temporal workflows

docker exec temporal-admin-tools sh -c '
  workflows=$(tctl workflow list --open --pjson --pagesize 1000 2>/dev/null)
  if ! count=$(echo "$workflows" | jq length 2>/dev/null); then
    echo "Failed to parse workflow list"
    exit 1
  fi
  if [ -z "$count" ] || [ "$count" -eq 0 ]; then
    echo "No open workflows found"
    exit 0
  fi
  echo "Found $count open workflow(s)"
  echo "$workflows" | jq -r ".[].execution.workflowId" | \
  while read wf_id; do
    echo "Terminating: $wf_id"
    if ! tctl workflow terminate --workflow_id "$wf_id" 2>&1; then
      echo "  Terminate failed, using admin delete..."
      tctl admin workflow delete --workflow_id "$wf_id" --yes
    fi
  done
'
