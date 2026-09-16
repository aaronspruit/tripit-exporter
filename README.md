# tripit-exporter

tripit-exporter keeps a local archive of your TripIt trips: one JSON file for each trip, one ICS file for each trip, and one ICS file for all trips. A scheduled run reads the TripIt private calendar feed, and a one-time backfill reads the TripIt web API to add the older trips.

## Set up the calendar feed

1. On the TripIt website, turn on "Display individual plans within a trip" in the Calendar Feed settings. The TripIt app calls the same setting "Include detailed items".
2. Copy the private feed URL. It has the form `https://www.tripit.com/feed/ical/private/<key>/tripit.ics`. Treat it as a credential: anyone with the URL can read your trips.

## Get the image

The image is `ghcr.io/aaronspruit/tripit-exporter`, for `linux/amd64`. The `latest` tag points at the newest release. To control when you upgrade, use a version tag such as `0.1.0` in place of `latest`.

## Run with Docker Compose

1. Download the Compose file, and the example environment file as `.env`:

   ```bash
   curl -fsSLO https://raw.githubusercontent.com/aaronspruit/tripit-exporter/main/compose.yaml
   curl -fsSL -o .env https://raw.githubusercontent.com/aaronspruit/tripit-exporter/main/.env.example
   ```

2. In `.env`, set `TRIPIT_FEED_URL`.
3. Make the data folder, and give it to the container user. The container runs as UID 65532, and it cannot write to a folder that Docker makes as root.

   ```bash
   mkdir data && sudo chown 65532:65532 data
   ```

4. Run `docker compose run --rm tripit-exporter`.

The archive appears under `./data`.

## Run on Kubernetes

Download [`k8s/cronjob.yaml`](k8s/cronjob.yaml). Set the feed URL in its Secret, and pick a data volume as the file comments tell you. Then apply the file. The CronJob runs the container every 6 hours.

## Run from a host crontab

Copy the binary out of the image:

```bash
docker create --name tripit-exporter ghcr.io/aaronspruit/tripit-exporter:latest
docker cp tripit-exporter:/tripit-exporter /path/to/tripit-exporter
docker rm tripit-exporter
```

Then add a line such as this to the crontab of the user who owns the output folder:

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
    ├── <trip uuid>.json    one trip: its events, and its v2 objects once the backfill has read it
    └── <trip uuid>.ics     the events of one trip
```

Each run merges the feed into the archive by event `UID`, and it ignores a change to `DTSTAMP` alone. A trip that leaves the feed window stays in the archive; a trip or plan that TripIt deletes is removed from the archive on the next run. A trip file keeps a plan whose trip event did not appear in the same fetch, but the file does not gain that plan back until a later fetch holds the trip event too.

## Exit codes

| Code | Reason |
|---|---|
| `0` | Success, or a `429` rate limit. The next run continues |
| `1` | The feed URL is wrong or revoked, or the backfill cookie is invalid. Get a new one |
| `2` | Any other error |

## Backfill

Run the backfill once, to add the trips outside the feed window, and the structured fields of every trip:

1. In a browser, sign in to TripIt. Open the developer tools, and copy the value of the session cookie.
2. Run `docker compose run --rm -it tripit-exporter backfill`.
3. Paste the cookie at the prompt, then press Enter. The terminal does not show it, and the archive does not store it.

The backfill writes each trip file as soon as it finishes that trip. If it stops, run it again: it skips each trip that already has its structured fields and its events, so it picks up where it left off. If TripIt blocks the download of a trip, or sends a calendar that the backfill cannot read, the backfill shows a warning, keeps the structured fields of that trip, and continues. The next run tries that download again.

## Limits

The feed holds the last 90 days and all future trips. A plan's detail from the feed is free text; the structured fields come from the backfill.

## Development

```bash
go test ./...
```

Golden files under each package's `testdata/` hold expected output. Run `go test ./... -update` to write them instead of comparing against them, then review the diff. See [CLAUDE.md](CLAUDE.md) for the full command list and the CI checks a pull request must pass.
