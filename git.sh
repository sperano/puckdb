#!/bin/sh -e
install -m 600 /mnt/secrets/netrc /root/.netrc
DIR=/mnt/yfh-data/cache
OUT=$(git -C $DIR status --porcelain)
if [ -z "$OUT" ]; then
  echo "Nothing to do"
else
  echo "Adding files"
  git -C $DIR add .
  git -C $DIR status
  git -C $DIR commit -am "new data"
  git -C $DIR push -u origin main
fi