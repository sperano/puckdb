# YFH

A Go application that imports and manages fantasy hockey data from Yahoo's Fantasy Sports API. It also downloads daily NHL game data (schedules, boxscores, player stats). Data is stored in PostgreSQL with Temporal handling workflow orchestration.

Includes a file-based caching system for API responses.

## Dependencies

- [nhl-api-go](https://github.com/sperano/nhl-api-go) - Go client library for the NHL Stats API
