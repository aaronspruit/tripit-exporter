# tripit-exporter

tripit-exporter keeps a local archive of your TripIt trips: one JSON file for each trip, one ICS file for each trip, and one ICS file for all trips. A scheduled run reads the TripIt private calendar feed, and a one-time backfill reads the TripIt web API to add the older trips.

## Status

Phase 2 of [the plan](docs/plan.md): the scheduled feed run and the archive. `tripit-exporter` with no argument fetches the feed and updates the archive. The backfill does not exist yet.

## Set up the calendar feed

1. On the TripIt website, turn on "Display individual plans within a trip" in the Calendar Feed settings. The TripIt app calls the same setting "Include detailed items".
2. Copy the private feed URL. It has the form `https://www.tripit.com/feed/ical/private/<key>/tripit.ics`. Treat it as a credential: anyone with the URL can read your trips.

## Run with Docker Compose

1. Copy `.env.example` to `.env`, and set `TRIPIT_FEED_URL`.
2. Run `docker compose run --rm tripit-exporter`.

The archive appears under `./data`.

## Run on Kubernetes

Apply [`k8s/cronjob.yaml`](k8s/cronjob.yaml). It stores the feed URL in a Secret, and it runs the container every 6 hours. Edit the image name, and pick a data volume as the file comments.

## Run from a host crontab

Build the static binary, then add a line such as this to the crontab of the user who owns the output folder:

```cron
0 */6 * * * TRIPIT_FEED_URL=... OUTPUT_DIR=/path/to/data /path/to/tripit-exporter
```

## Configuration

| Variable | Default | `/run/secrets` name | Description |
|---|---|---|---|
| `TRIPIT_FEED_URL` | none, required | `tripit_feed_url` | The private feed URL |
| `OUTPUT_DIR` | none, required | | Folder for the archive |

A Docker or Kubernetes secret file under `/run/secrets/<name>` takes priority over the matching environment variable.

## Output

```text
data/
├── tripit.ics              the events of every trip in the archive
└── trips/
    ├── <trip uuid>.json    one trip: its events, and its v2 objects once the backfill exists
    └── <trip uuid>.ics     the events of one trip
```

Each run merges the feed into the archive by event `UID`, and it ignores a change to `DTSTAMP` alone. A trip that leaves the feed window stays in the archive; a trip or plan that TripIt deletes is removed from the archive on the next run. A trip file keeps a plan whose trip event did not appear in the same fetch, but the file does not gain that plan back until a later fetch holds the trip event too.

## Exit codes

| Code | Reason |
|---|---|
| `0` | Success, or a `429` rate limit. The next run continues |
| `1` | The feed URL is wrong or revoked. Copy a new one from TripIt |
| `2` | Any other error |

## Limits

The feed holds the last 90 days and all future trips. A plan's detail is free text; the structured fields come from the backfill, once it exists.

## Development

```bash
go test ./...
```

Golden files under each package's `testdata/` hold expected output. Run `go test ./... -update` to write them instead of comparing against them, then review the diff. See [CLAUDE.md](CLAUDE.md) for the full command list and the CI checks a pull request must pass.
