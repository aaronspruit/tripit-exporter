# tripit-exporter

tripit-exporter keeps a local archive of your TripIt trips: one JSON file for
each trip, one ICS file for each trip, and one ICS file for all trips.

There are two ways to run it:

1. [Part 1: the calendar feed](#part-1-the-calendar-feed). The exporter reads
   the private TripIt calendar feed. It needs no sign-in. The feed holds all
   future trips and the last 90 days, and the detail of a plan is free text.
2. [Part 2: the full history](#part-2-the-full-history). The exporter also
   reads the TripIt JSON. It needs a session cookie from your browser. The
   JSON holds every trip in your account, and the structured fields of each
   plan.

Part 2 adds to Part 1. Set up the feed first, then add the cookie when you
want the older trips.

## Part 1: the calendar feed

### Set up the feed

1. On the TripIt website, turn on "Display individual plans within a trip" in
   the Calendar Feed settings. The TripIt app calls the same setting "Include
   detailed items".
2. Copy the private feed URL. It has the form
   `https://www.tripit.com/feed/ical/private/<key>/tripit.ics`. Treat it as a
   credential: anyone with the URL can read your trips.

### Get the image

The image is `ghcr.io/aaronspruit/tripit-exporter`, for `linux/amd64`. The
`latest` tag points at the newest release. To control when you upgrade, use a
version tag such as `0.2.0` in place of `latest`.

### Run with Docker Compose

1. Download the Compose file, and the example environment file as `.env`:

   ```bash
   curl -fsSLO https://raw.githubusercontent.com/aaronspruit/tripit-exporter/main/compose.yaml
   curl -fsSL -o .env https://raw.githubusercontent.com/aaronspruit/tripit-exporter/main/.env.example
   ```

2. In `.env`, set `TRIPIT_FEED_URL`.
3. Make the data folder, and give it to the container user. The container runs
   as UID 65532, and it cannot write to a folder that Docker makes as root.

   ```bash
   mkdir data && sudo chown 65532:65532 data
   ```

4. Run `docker compose run --rm tripit-exporter`.

The archive appears under `./data`.

### Run on Kubernetes

Download [`k8s/cronjob.yaml`](k8s/cronjob.yaml). Set the feed URL in its
Secret, and pick a data volume as the file comments tell you. Then apply the
file. The CronJob runs the container every 6 hours.

### Run from a host crontab

Copy the binary out of the image:

```bash
docker create --name tripit-exporter ghcr.io/aaronspruit/tripit-exporter:latest
docker cp tripit-exporter:/tripit-exporter /path/to/tripit-exporter
docker rm tripit-exporter
```

Then add a line such as this to the crontab of the user who owns the output
folder:

```cron
0 */6 * * * TRIPIT_FEED_URL=... OUTPUT_DIR=/path/to/data /path/to/tripit-exporter
```

Each run merges the feed into the archive by event `UID`, and it ignores a
change to `DTSTAMP` alone. A trip that leaves the 90-day feed window stays in
the archive. A trip or plan that TripIt deletes is removed from the archive on
the next run.

## Part 2: the full history

The exporter reads the TripIt JSON with one cookie value, `it_session_id`.
TripIt sets that cookie when you sign in with "Keep me signed in". The cookie
is valid for 15 days, and each run gives it 15 more days. Treat it as a
credential: it gives full access to your TripIt account.

### Copy the session cookie

1. Open a private browser window, and go to
   `https://www.tripit.com/account/login`.
2. Sign in with your TripIt email and password, and select "Keep me signed
   in". TripIt sets `it_session_id` for a password sign-in only. A Google or
   an Apple sign-in does not set it.
3. Open the developer tools of your browser, and find the cookies of
   `https://www.tripit.com`. The steps for each browser follow.
4. Copy the value of `it_session_id`. Copy the value alone, and not the name.
5. Close the private window. A sign-out does not stop the value, so the copied
   value stays valid.

#### Firefox

1. Press F12. You can also select the menu, then More tools, then Web
   Developer Tools.
2. Select the Storage tab.
3. Open Cookies, then `https://www.tripit.com`.
4. Select the row `it_session_id`, and copy the Value column.

#### Chrome, Edge, Brave and the other Chromium browsers

1. Press F12. You can also select the menu, then More tools, then Developer
   tools.
2. Select the Application tab.
3. Open Storage, then Cookies, then `https://www.tripit.com`.
4. Select the row `it_session_id`, and copy the Cookie Value box below the
   table.

#### Safari

1. Select Safari, then Settings, then Advanced. Turn on "Show features for web
   developers".
2. Press Option-Command-I. You can also select Develop, then Show Web
   Inspector.
3. Select the Storage tab.
4. Open Cookies, then `tripit.com`.
5. Select the row `it_session_id`, and copy the Value column.

### Turn on the JSON refresh

1. Set `TRIPIT_JSON_REFRESH` to `true`.
2. Set `TRIPIT_SESSION` to the cookie value. In `.env`, remove the comment
   marks from those two lines. On Kubernetes, put `TRIPIT_SESSION` in the
   Secret, and remove the comment marks in `k8s/cronjob.yaml`.
3. If the browser you copied the cookie from is not Firefox 155 on Windows,
   set `TRIPIT_USER_AGENT` to the `User-Agent` of that browser.
4. Run the exporter as in Part 1.

### The first run

The first run reads every trip in your account. TripIt accepts about 50
requests in 10 minutes, and the run sends two requests for each trip, so 200
trips take about 80 minutes. When TripIt holds a request with no answer, the
run shows a line and waits up to 8 minutes before it tries again. The run
writes each trip file as soon as it finishes that trip. If the run stops, the
next run continues where it stopped.

Each later run reads the JSON of each future trip, of each trip that ended in
the last `TRIPIT_JSON_REFRESH_LOOKBACK_DAYS` days, and of each trip that the
archive does not hold yet. TripIt shows a change to the plans of a trip only
in the JSON of that trip, so the run reads each trip in that window. A trip
file changes only when the TripIt data changes. The feed gives the events of
these trips, so the run does not download their events again.

### Keep the session current

TripIt gives a new `it_session_id` value to each new session, with an expiry
of 15 days. The exporter writes the newest value to `data/.tripit-session`
with mode `0600`, and it uses that file before `TRIPIT_SESSION`. Run the
exporter at least once every 15 days. If TripIt rejects both values, the run
exits with `1`. Copy a new cookie value into `TRIPIT_SESSION`. The state file
gives full access to your TripIt account, so do not share the data folder.

## Environment variables

| Variable | Used by | Required | Default | `/run/secrets` name | Description |
|---|---|---|---|---|---|
| `TRIPIT_FEED_URL` | Feed run | Yes | none | `tripit_feed_url` | The private feed URL. Treat it as a credential |
| `OUTPUT_DIR` | Feed run, backfill | No | `/data` | | The folder for the archive |
| `TRIPIT_JSON_REFRESH` | Feed run | No | `false` | | When `true`, each run also reads the TripIt JSON. See [Part 2](#part-2-the-full-history) |
| `TRIPIT_JSON_REFRESH_LOOKBACK_DAYS` | Feed run | No | `7` | | The number of days after a trip ends that the run still reads its JSON. The run reads each future and current trip every time. It does not read a trip that ended more than this number of days ago, unless the archive does not hold the JSON and the events of that trip yet. With `0`, the run stops on the end date of a trip |
| `TRIPIT_SESSION` | Feed run | When `TRIPIT_JSON_REFRESH` is `true` | none | `tripit_session` | The value of the TripIt cookie `it_session_id`. Treat it as a credential |
| `TRIPIT_USER_AGENT` | Feed run, backfill | No | Firefox 155 on Windows | | The `User-Agent` of the browser you copy the cookie from. The exporter sends it with every TripIt JSON request |
| `TRIPIT_VERBOSE` | Feed run, backfill | No | `false` | | When `true`, the exporter shows each TripIt JSON request and answer, with a timestamp. The cookie values stay hidden |
| `TRIPIT_AIRTRAIL_SYNC` | Feed run | No | `false` | | When `true`, each run also makes the flights of an AirTrail instance match the archive. See [The AirTrail sync](#the-airtrail-sync) |
| `TRIPIT_AIRTRAIL_URL` | Feed run | When `TRIPIT_AIRTRAIL_SYNC` is `true` | none | | The root URL of the AirTrail instance, for example `https://airtrail.example.com` |
| `TRIPIT_AIRTRAIL_API_KEY` | Feed run | When `TRIPIT_AIRTRAIL_SYNC` is `true` | none | `airtrail_api_key` | An AirTrail API key. Treat it as a credential |
| `TRIPIT_AIRTRAIL_USER_ID` | Feed run | No | the key holder | | The AirTrail user that owns each flight. Leave it empty to use the holder of the API key |
| `TRIPIT_AIRTRAIL_DELETE` | Feed run | No | `true` | | With `false`, the sync adds and updates a flight but never deletes one |

A Docker or Kubernetes secret file under `/run/secrets/<name>` takes priority
over the matching environment variable. The `backfill` subcommand reads its
cookie from a prompt, and never from an environment variable.

## Output

```text
data/
├── .tripit-session         the newest TripIt session value, when the JSON refresh is on
├── airtrail-state.json     the AirTrail flight of each TripIt segment, when the AirTrail sync is on
├── tripit.ics              the events of every trip in the archive
└── trips/
    ├── <trip uuid>.json    one trip: its events, and its structured fields once the JSON refresh has read it
    └── <trip uuid>.ics     the events of one trip
```

A trip file keeps a plan whose trip event did not appear in the same fetch,
but the file does not gain that plan back until a later fetch holds the trip
event too.

## Exit codes

| Code | Reason |
|---|---|
| `0` | Success, or a `429` rate limit. The next run continues |
| `1` | The feed URL is wrong or revoked, or TripIt rejected the session value or the backfill cookie. Get a new one |
| `2` | Any other error |
| `3` | The AirTrail sync failed. A rejected API key has its own code, and not `1`, so that the two systems stay apart |

## The AirTrail sync

With `TRIPIT_AIRTRAIL_SYNC` set to `true`, each run makes the flights of an
AirTrail instance match the air segments of the archive. The sync runs after
the merge and the JSON refresh, and it reads the structured `v2` object of
each trip, so a trip gives flights only once the refresh or the backfill has
read it. Make the API key in AirTrail under Settings.

`data/airtrail-state.json` holds the AirTrail flight id of each TripIt
segment, with a hash of the body that made it:

- A segment with no entry is added.
- A segment whose hash matches sends no request at all. A run that changes
  nothing in TripIt changes nothing in AirTrail, so a change that you make in
  AirTrail stays until TripIt changes the same flight.
- A segment whose hash differs replaces the flight of its stored id.
- An entry with no segment is deleted. This covers a cancelled trip and a
  cancelled leg of a trip that goes ahead.

The sync deletes only a flight that it added, because the state file holds
the only list of those ids. A flight that you add in AirTrail is never
touched.

AirTrail matches an airline and an aircraft type by ICAO code, and TripIt
gives an IATA code, so the exporter holds a table for each. A code that no
table holds makes a warning, and the flight keeps no airline or no aircraft.
The exporter never guesses, because AirTrail accepts a wrong airline or a
wrong aircraft without a word. An airport needs no table: the AirTrail API
accepts an IATA airport code.

A flight that AirTrail refuses makes a warning and no state entry, so the
next run tries it again. Only a rejected API key stops the run.

## The backfill subcommand

Use the JSON refresh of [Part 2](#part-2-the-full-history) instead of this
subcommand. `backfill` reads the same trips one time, but it needs the full
`Cookie` header of a browser request, and a person at a terminal.

1. In a browser, sign in to TripIt with your TripIt email and password, and
   select "Keep me signed in".
2. Open the developer tools, and open the Network tab. Load a TripIt page, and
   select a request to `www.tripit.com`. Copy the full value of its `Cookie`
   request header. The backfill needs all the cookies in that header, and not
   the session cookie alone. If your browser is not Firefox 155 on Windows,
   also copy the `User-Agent` request header into `TRIPIT_USER_AGENT`.
3. Run `docker compose run --rm -it tripit-exporter backfill`.
4. Paste the cookie at the prompt, then press Enter. The terminal does not
   show it, and the archive does not store it.
5. When the backfill shows `done`, delete the copied cookie from the
   clipboard. A sign-out in the browser does not stop the `it_session_id`
   value in that cookie.

If TripIt blocks the download of a trip, or sends a calendar that the backfill
cannot read, the backfill shows a warning, keeps the structured fields of that
trip, and continues. The next run tries that download again.

## Development

```bash
go test ./...
```

Golden files under each package's `testdata/` hold expected output. Run
`go test ./... -update` to write them instead of comparing against them, then
review the diff. See [CLAUDE.md](CLAUDE.md) for the full command list and the
CI checks a pull request must pass.
