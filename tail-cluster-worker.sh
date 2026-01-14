#!/bin/bash
stern -n yfh2022 yahoo-hockey-fantasy-worker -t --since 10m -e '\/ping' -e '\/metrics'