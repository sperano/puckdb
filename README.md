# yahoo-fantasy-hockey

My Apps
Pool Crapettes

App ID
FxxNaGdu

Client ID (Consumer Key)
***REMOVED***

Client Secret (Consumer Secret)
***REMOVED***

## commands

`yfh cache udate` downloads the different xml files that are missing. without parameters, it will check from the beginning of the season to the current date.

    yfh cache update # update from the beginning to today
    yfh cache update 11-20 # update on that specific date
    yfh cache update --fantasy-game # only download the fantasy game file
    yfh cache update --from 11-20
    yfh cache update --from 11-20 --to 11-25
    yfh cache update --to 11-25

You can add `-f` to force new downloads.

`yfh cache import` works like update, same flags. but it imports the xml into the database, if the data is not there yet:

    yfh cache import
    yfh cache import --date 11-20
    yfh cache import --from 11-20
    yfh cache import --from 11-20 --to 11-25
    yfh cache import --to 11-25

import will import the players found that are missing. it's possible to only import player and skipping roster data import:

    yfh cache import --players
    yfh cache import --players --from 11-20
    etc...

update and import always check for fantasy_game (is it downloaded, is it imported)

`yfh cache fantasy-game` displays the different attritutes ("hockey", etc.) of the downloaded fantasy game xml file.

`yfh cache team 7 11-20` display the roster from the xml downloaded for this team id and date.

`yfh db team 7 11-20` display the data in the database for the team on this date.

`yfh db player 342` display the data in the database for the player.


## Jupyterhub 

https://towardsdatascience.com/heres-how-to-run-sql-in-jupyter-notebooks-f26eb90f3259


