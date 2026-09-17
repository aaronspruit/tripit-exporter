# tripit-exporter

tripit-exporter keeps a local archive of your TripIt trips: one JSON file for each trip, one ICS file for each trip, and one ICS file for all trips. A scheduled run reads the TripIt private calendar feed, and a one-time backfill reads the TripIt web API to add the older trips. The scheduled run can also keep the JSON of recent and future trips current.

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

## Environment variables

| Variable | Used by | Required | Default | `/run/secrets` name | Description |
|---|---|---|---|---|---|
| `TRIPIT_FEED_URL` | Feed run | Yes | none | `tripit_feed_url` | The private feed URL. Treat it as a credential |
| `OUTPUT_DIR` | Feed run, backfill | No | `/data` | | The folder for the archive |
| `TRIPIT_JSON_REFRESH` | Feed run | No | `false` | | When `true`, each run also refreshes the JSON of the trips. See [Refresh the trip JSON](#refresh-the-trip-json) |
| `TRIPIT_JSON_REFRESH_LOOKBACK_DAYS` | Feed run | No | `7` | | The number of days after a trip ends that the refresh still reads its JSON. The refresh reads each future and current trip at each run. It does not read a trip that ended more than this number of days ago, unless the archive does not have the JSON and the events of that trip yet. With `0`, the refresh stops on the end date of a trip |
| `TRIPIT_SESSION` | Feed run | When `TRIPIT_JSON_REFRESH` is `true` | none | `tripit_session` | The value of the TripIt cookie `it_session_id`. Treat it as a credential |
| `TRIPIT_USER_AGENT` | Backfill, refresh | No | Firefox 155 on Windows | | The `User-Agent` of the browser you copy the cookie from. The backfill and the refresh send it with every request |
| `TRIPIT_VERBOSE` | Backfill, refresh | No | `false` | | When `true`, the backfill and the refresh show each request and response, with a timestamp. The cookie values stay hidden |

The backfill reads the session cookie from its prompt, and never from an environment variable.

A Docker or Kubernetes secret file under `/run/secrets/<name>` takes priority over the matching environment variable.

## Output

```text
data/
├── .tripit-session         the newest TripIt session value, when the JSON refresh is on
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
| `1` | The feed URL is wrong or revoked, or TripIt rejected the backfill cookie or the refresh session. Get a new one |
| `2` | Any other error |

## Backfill

Run the backfill once, to add the trips outside the feed window, and the structured fields of every trip:

1. In a browser, sign in to TripIt with your TripIt email and password, and select "Keep me signed in". The cookie then stays valid if you must run the backfill again later. The box has no effect when you sign in with Google or another outside account.
2. Open the developer tools, and open the Network tab. Load a TripIt page, and select a request to `www.tripit.com`. Copy the full value of its `Cookie` request header. The backfill needs all the cookies in that header, and not the session cookie alone. If your browser is not Firefox 155 on Windows, also copy the `User-Agent` request header into `TRIPIT_USER_AGENT`.
3. Run `docker compose run --rm -it tripit-exporter backfill`.
4. Paste the cookie at the prompt, then press Enter. The terminal does not show it, and the archive does not store it.
5. When the backfill shows `done`, delete the copied cookie from the clipboard. A sign-out in the browser does not stop the `it_session_id` value in that cookie.

TripIt accepts about 50 requests in 10 minutes, and the backfill sends two for each trip, so 200 trips take about 80 minutes. When TripIt holds a request with no response, the backfill shows a line and waits up to 8 minutes before it tries again. The backfill writes each trip file as soon as it finishes that trip. If it stops, run it again: it skips each trip that already has its structured fields and its events, so it picks up where it left off. If TripIt blocks the download of a trip, or sends a calendar that the backfill cannot read, the backfill shows a warning, keeps the structured fields of that trip, and continues. The next run tries that download again.

## Refresh the trip JSON

The scheduled run can also read the JSON of the trips that can still change. The refresh is off by default. To turn it on:

1. In a private browser window, sign in to TripIt with your TripIt email and password, and select "Keep me signed in".
2. Open the developer tools. In Firefox, open Storage, then Cookies, then `https://www.tripit.com`. In Chrome, open Application, then Cookies. Copy the value of `it_session_id`.
3. Set `TRIPIT_SESSION` to that value, and set `TRIPIT_JSON_REFRESH=true`. On Kubernetes, put `TRIPIT_SESSION` in the Secret.
4. Close the private window.

After the feed merge, the refresh lists the trips. It reads the JSON of each future trip, each trip that ended in the last `TRIPIT_JSON_REFRESH_LOOKBACK_DAYS` days, and each trip that the archive does not hold yet. TripIt shows a change to the plans of a trip only in the JSON of that trip, so the refresh reads each trip in that window. A trip file changes only when the TripIt data changes. The feed gives the events of these trips, so the refresh does not download their events again. A first refresh with no backfill reads every trip, and takes as long as the backfill. If a run stops, the next run continues.

TripIt gives a new `it_session_id` value to each new session, with an expiry of 15 days. The refresh writes the newest value to `data/.tripit-session` with mode `0600`, and uses that file before `TRIPIT_SESSION`. Run the refresh at least once every 15 days. If TripIt rejects both values, the run exits with `1`: copy a new value into `TRIPIT_SESSION`. The file gives full access to your TripIt account, so do not share the data folder. A sign-out in the browser does not stop the value.

## Limits

The feed holds the last 90 days and all future trips. A plan's detail from the feed is free text; the structured fields come from the backfill and the JSON refresh.

## Development

```bash
go test ./...
```

Golden files under each package's `testdata/` hold expected output. Run `go test ./... -update` to write them instead of comparing against them, then review the diff. See [CLAUDE.md](CLAUDE.md) for the full command list and the CI checks a pull request must pass.
