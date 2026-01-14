 #!/bin/bash
stern -n yfh2022 yahoo-hockey-fantasy-api -t --since 10m -e '\/ping' -e '\/metrics'
