# Yahoo API access check

Yahoo answers `403 "This application is not authorized to perform this
action"` for the 2026 season while the app's access request is pending. Until
Yahoo changes that, the 2026 leagues import stand-in settings through
`temporary_metadata_from` (see `config.LeagueMetadataSource`).
`puckdb yahoo check-access` finds out whether Yahoo serves the season yet, and
can email the answer. The intended use is a daily Kubernetes CronJob.

## What it checks

It makes the same calls the importer makes, using the stored OAuth token:

1. It resolves the season's game key (`games;game_codes=nhl;seasons=<season>`).
2. If the game key resolves, it fetches the settings of every league the Yahoo
   seasons config lists for that season (`league/<gameKey>.l.<id>/settings`).
   If the game key does not resolve, the leagues are listed as not checked.

`--yahoo-check-season` picks the season. The default is 0, which means the
latest season in the Yahoo seasons config.

| Outcome | Meaning | Exit code |
| --- | --- | --- |
| `AUTHORIZED` | Every call returned 200 | 0 |
| `NOT AUTHORIZED` | Every failed call was a 403 (Yahoo's error text is in the report) | 0 |
| `ERROR` | The check could not decide. Examples: no seasons config, no leagues for the season, no token or refresh rejected (sign in again at `/yahoo/login`), a network error, another status, an unreadable game key | 1 |

The command prints the report. When `--notify-email-to` is set, it also emails
the report. The subject carries the outcome, so you can read it without
opening the email:

```
[puckdb] Yahoo app AUTHORIZED for 2026 (2/2 leagues) - 2026-10-01
[puckdb] Yahoo app NOT authorized yet for 2026 (0/2 leagues) - 2026-10-01
[puckdb] Yahoo access check ERROR - 2026-10-01
```

The subject also carries the date (UTC), so mail clients do not thread every
day's email into one conversation. If the email cannot be sent, the command
exits 1. An invalid email configuration (missing host or sender, bad address)
is rejected before Yahoo is called.

## Configuration

Like every other flag, each of these can also be set as a `PUCKDB_*`
environment variable.

| Flag | Env var | Default | Notes |
| --- | --- | --- | --- |
| `--yahoo-check-season` | `PUCKDB_YAHOO_CHECK_SEASON` | `0` | 0 = latest configured season |
| `--notify-email-to` | `PUCKDB_NOTIFY_EMAIL_TO` | empty | Comma-separated recipients; empty = print only |
| `--smtp-host` | `PUCKDB_SMTP_HOST` | empty | SMTP submission server |
| `--smtp-port` | `PUCKDB_SMTP_PORT` | `587` | Implicit TLS (port 465) is not supported |
| `--smtp-username` | `PUCKDB_SMTP_USERNAME` | empty | Empty = no authentication (internal relay) |
| `--smtp-password` | `PUCKDB_SMTP_PASSWORD` | empty | Redacted from logs |
| `--smtp-from` | `PUCKDB_SMTP_FROM` | empty | Sender address |

The command uses STARTTLS whenever the server offers it. It refuses to
authenticate over an unencrypted connection, except to localhost.

The check also needs what the worker uses to reach Yahoo:

- Redis (`PUCKDB_REDIS_*`), which holds the OAuth token. A refused
  connection is retried 3 times, 5 seconds apart, before the check reports
  ERROR: a new pod can be refused by a NetworkPolicy until its IP reaches the
  allow list. A reply from Redis, such as a bad password, is not retried.
- The Yahoo OAuth client (`PUCKDB_YAHOO_OAUTH2_CLIENT_ID` / `_SECRET`), used to
  refresh an expired access token.
- The Yahoo seasons config (`PUCKDB_YAHOO_SEASONS`).
- `PUCKDB_PUBLIC_URL` is optional. When set, a missing-token error includes
  the full login URL.

## Scheduling

The CronJob lives with the deploy manifests, outside this repository. It runs
the worker image with `args: ["yahoo", "check-access"]`, the environment above
and the seasons config mounted from its ConfigMap; it needs no database or data
volume. Use `concurrencyPolicy: Forbid`, `backoffLimit: 0` and
`restartPolicy: Never`, so a failed run does not retry and send a second email
the same day. To test it right after applying it:

```bash
kubectl create job --from=cronjob/<cronjob-name> yahoo-access-check-manual
```

Once the report says `AUTHORIZED`:

1. Remove `temporary_metadata_from` from the season's leagues.
2. Run a sync. The real settings replace the stand-in.
3. Delete the CronJob.

## Why not Prometheus / Alertmanager

The goal is a daily email saying whether the app is authorized, whatever the
answer. Alertmanager is a poor fit for that:

- Alertmanager notifies when an alert's state changes (firing or resolved).
  This report would be an alert that fires every day for weeks.
- Alertmanager needs its own SMTP receiver and subject templates.
- Prometheus cannot scrape a one-shot job without a Pushgateway.

If kube-state-metrics alerts are installed (for example `KubeJobFailed`), they
already flag a run that exits 1. A possible follow-up, if alerting is ever
wanted: a `puckdb_yahoo_app_authorized` gauge in the `metrics` command, plus an
alert rule.
