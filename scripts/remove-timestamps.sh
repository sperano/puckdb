#!/bin/bash

# Script to remove timestamps from versioned files in yfh-data
# Uses git mv to preserve file history

set -e

DATA_DIR="../yfh-data"

if [ ! -d "$DATA_DIR" ]; then
    echo "Error: $DATA_DIR does not exist"
    exit 1
fi

cd "$DATA_DIR"

if [ ! -d ".git" ]; then
    echo "Error: $DATA_DIR is not a git repository"
    exit 1
fi

# Find all files with a 14-digit timestamp suffix (YYYYMMDDHHmmss)
# Pattern: anything ending in .NNNNNNNNNNNNNN where N is a digit
count=0
while IFS= read -r -d '' file; do
    # Get the new name by removing the timestamp suffix
    newname="${file%.[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]}"

    if [ "$file" != "$newname" ]; then
        # Check if target already exists
        if [ -e "$newname" ]; then
            echo "Warning: $newname already exists, skipping $file"
            continue
        fi

        echo "git mv '$file' -> '$newname'"
        git mv "$file" "$newname"
        ((count++))
    fi
done < <(find . -type f -regex '.*\.[0-9]\{14\}$' -print0)

echo ""
echo "Renamed $count files"
echo "Run 'git status' to review changes, then 'git commit' to save"
