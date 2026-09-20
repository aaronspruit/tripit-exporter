<!-- harness:rules:begin -->
Written by harness from rules/. Do not edit between these two markers.
https://github.com/aaronspruit/harness

### Write in Simplified Technical English

Write all documentation in Simplified Technical English (ASD-STE100). This
covers the README, the agent instruction file, comments and docstrings, commit
messages, pull request titles and bodies, release notes, and each message that a
person reads. Apply the rules before you draft, and not after.

The standard is free at https://asd-ste100.org. If the session holds the
`simple-english` skill, invoke it first.

### Keep breaking-change detail in the pull request

Do not put migration steps, upgrade instructions, or "this breaks X" in the
README or in the agent instruction file. Put them in the pull request body. If
the repository uses `changelog:` labels, apply the correct one. That label
carries the detail into the release notes.

The README and the agent instruction file describe how the code works now. The
release notes are the only record of what changed. A design note can say why the
current behavior exists. It must not tell a user what to do about an older
install.

### Write comments about the present, not the past

A comment says what the current code does, and why. It does not say what the
code replaced, or what an older version did. `git log` and `git blame` hold
that, and a stale "X still serves Y" line is wrong the moment Y changes.

When you migrate something, write the new file as if it was always that way.
Strip the same kind of history out of every file that the change touches. The
migration story goes in the commit message and in the pull request.

### Write the fewest sentences that carry the fact

State each fact in one place, and link to that place from anywhere else that
needs it. Do not repeat what a linked file already says. Do not write a
preamble, a list of what comes next, or a closing restatement. If a section
changes nothing about what the reader does, delete the section instead of
shortening it.

### Give the recommendation first

When the work needs a decision, or when you compare options, write the
recommendation in the first sentence. Then give the reasons. Then give the one
fact that argues against it. Do not spread the recommendation through the
analysis. Do not end with a list of options and no choice.

This rule applies to an answer in a session, to a pull request body, and to a
design document.

### Write what you find back to the issue

When you read an issue to plan work, add a comment to that issue. Add a second
comment when you make a branch for it. The comment holds what you decided, why,
what the work covers, and what it leaves out. It holds no install step. Those go
in the pull request, under the rule for breaking changes.

When a later finding changes the answer, comment again. Do not leave the old
comment standing alone. An issue that carries no comment makes the next session
do the same research again.
<!-- harness:rules:end -->

## Commands

```bash
# Run the full test suite with coverage (coverage must stay >= 80%)
go test -race -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Run a single package or test
go test ./internal/tripittest/...
go test ./cmd/tripit-exporter/ -run TestRunVersion

# Update golden files instead of comparing against them
go test ./... -update

# Format-check and lint
test -z "$(gofmt -l .)"
go vet ./...
golangci-lint run

# Build the image locally
docker build -t tripit-exporter:test .
```

## Architecture

`cmd/tripit-exporter` parses the subcommand. With no subcommand, it fetches
the feed and updates the archive, then runs the JSON refresh when
`TRIPIT_JSON_REFRESH` is true; `version` prints the version; `backfill` runs
the one-time backfill.

| Package | Holds |
|---|---|
| `internal/secret` | Reads a credential from `/run/secrets/<name>`, then from an environment variable |
| `internal/ics` | Reads and writes the VEVENT blocks of an ICS calendar, keeping each property as raw text |
| `internal/feed` | Fetches the calendar feed, and maps each HTTP result to an error type |
| `internal/archive` | Loads, merges and writes the trip files, and makes the two ICS files |
| `internal/tripitweb` | Calls the TripIt web API v2 and the trip download URL with a session cookie |
| `internal/airtrail` | Turns the `v2` air segments into AirTrail flights, and keeps an AirTrail instance in step with the archive |

The exit code order: a feed fetch error takes the code from
[the table](README.md#exit-codes) before the archive runs at all. An archive
error, for example a disk failure, exits with `2`. A setting error of the
refresh or of the AirTrail sync exits with `2` before the feed fetch. The
refresh runs only after a successful merge, and its error takes the backfill
codes. The AirTrail sync runs after the refresh, and every failure of it
exits with `3`.

## The archive

A trip file holds its events and, once the backfill exists, its `v2`
object. The merge compares an event with its archived copy and ignores
`DTSTAMP`, so a fetch that only refreshes the timestamp writes nothing. This
keeps a run idempotent: the archive changes only when TripIt data changes.

The `in_feed` flag limits the delete rules to trips that came from the feed.
The backfill will read trips that other travelers share, and the feed does
not hold those, so `in_feed: false` keeps them safe from deletion.

A trip absent from a fetch stays in the archive unless it ended more than 83
days before the fetch, which the 7-day margin around the 90-day feed window
allows for. A kept trip is a visible mistake that a person can correct; a
deleted trip is a silent loss.

Every write goes to a temporary file in the same folder, then `os.Rename`,
and only when the SHA-256 of the new content differs from the file on disk.
A crash before the rename leaves the old file whole.

## The backfill

`internal/tripitweb.Client` sends the cookie as the `Cookie` header of every
request, with the `User-Agent` of `TRIPIT_USER_AGENT` or `DefaultUserAgent`
and the headers of a Firefox request on the TripIt website. A web API v2
route also gets the headers of the website's own API call
(`apiHeaders`); the download URL gets the headers of a browser navigation
(`downloadHeaders`), because TripIt returns `403` to a request without
them. The header set and the `<uuid>.ics` file name work against the real
account from the operator machine; `docs/research.md` open questions 1 and 2
record what the real run did not test.

The v2 trip object holds no numeric trip ID, so the backfill reads
`trip_id` from the TripIt link in the downloaded trip event.

The client waits 5 seconds before each request after the first (`pace`).
TripIt throttles in four forms: a `429`, a TCP reset, an HTTP/2 protocol
error, or a request that passes the 20-second `requestTimeout`. On each of
them the client waits 1, 2, 4, then 8 minutes (`throttleWaits`), writes a
line through `Client.Logf` before each wait, and sends the same request
again. The client retries a `401` once after 5 seconds; a second `401`
becomes an `*AuthError`, and a request that TripIt still throttles after
the last wait becomes a `*RateLimitedError`. `cmd` turns the first into
exit code `1` and the second into exit code `0`. `TRIPIT_VERBOSE=true` sets
`Client.Verbose`, which writes each request and response through `Logf`
and hides the value of `Cookie` and of each `Set-Cookie`.

`cmd/tripit-exporter backfill` writes each trip file as soon as it finishes
that trip, and skips a trip whose archived file already holds a `v2`
object and either events or `empty_download: true`, so a second run
resumes where the first stopped. A download status other than `200` or
`429` becomes a `*tripitweb.DownloadError`. That error, or a downloaded
calendar that does not parse, makes the run write a warning, keep the `v2`
object of that trip with no events, and continue; the next run tries that
download again. A download that holds zero events sets `empty_download:
true`, so the next run skips that trip. The flag records a success and not
a failure, so a trip file with no key gets the download. `backfillSleep` replaces the real wait in a `cmd` test. A trip UUID that
`archive.ValidTripUUID` rejects gets a warning and no request. The backfill
prompts for the cookie on stdin and, when stdin is a terminal, turns off the echo and canonical mode with the
Linux ioctl in `cmd/tripit-exporter/terminal_linux.go`; canonical mode cuts
a line at 4095 bytes, and a full `Cookie` header can be longer. The cookie exists
only in that prompt, in `Client.Cookie`, and in the cookie list that the
client updates from each `Set-Cookie` header, as a browser does; it never reaches a file, a log
line, or an error message.

## The JSON refresh

The user-facing names say "JSON" and never "v2", because a user does not
know the TripIt API version.

`cmd/tripit-exporter/refresh.go` opens a session with `it_session_id`
alone: first the value in `<OUTPUT_DIR>/.tripit-session`, then
`TRIPIT_SESSION`. TripIt answers a request with `it_session_id` and no
`session_id` with a new `session_id` and a new `it_session_id` that expires
in 15 days, so the refresh writes the value of `Client.CookieValue` to the
state file after the profile request and at the end of the run. The state
file is the only file that holds a TripIt credential. A `401` after the
retry, or a `500`, from the profile request rejects a value, because TripIt
returned `500` for a value that it did not accept; the refresh then tries
the next value, and exits with `1` when none remains.
`docs/research.md` holds the tests behind each of these rules.

The refresh reads each trip that `needsBackfill` selects, and each trip
whose `end` is on or after the run date minus
`TRIPIT_JSON_REFRESH_LOOKBACK_DAYS` days, or empty. The `last_modified` of a trip does not change when its
plans change, so the list cannot show a changed trip. A trip that holds
events or `empty_download: true` gets no download, because the feed holds
the events of each trip that ended in the last 83 days. `sameV2` ignores
the top-level `timestamp` of a detail response, which changes with each
request, so an unchanged trip writes no file.

`TRIPIT_WEB_BASE_URL` replaces the TripIt host that `internal/tripitweb`
calls. It is empty in production; a test sets it to a fake server's URL.

## The AirTrail sync

`internal/airtrail.Build` makes one request body for each `Segment` of each
`AirObject` of each trip, keyed by the TripIt segment UUID. It skips a hidden
segment and a trip that another traveler shares. `Sync` then adds, replaces
or deletes a flight so that AirTrail matches that set.

`<OUTPUT_DIR>/airtrail-state.json` holds the AirTrail flight id and a hash of
the body for each segment. The state cannot live in a trip file:
`shouldDeleteAbsentTrip` removes the file of a trip that left the feed, which
is the moment the sync needs the id to delete the flight. An equal hash sends
no request, so a change made in AirTrail stays until TripIt changes the same
flight. The state is written after each change, because it holds the only
record of a new flight.

`POST /api/flight/save` reads the date from `departure` and the clock time
from `departureTime`, and merges the two in the timezone of its own airport
record. An arrival date with no arrival time is a `400`, and so is an arrival
that is not after its departure, which one TripIt segment of the operator
archive holds. That flight keeps its departure and loses its arrival.

AirTrail matches an airline and an aircraft type by ICAO code alone, with no
IATA or name fallback, so `codes.go` holds a table for each. A generic TripIt
aircraft code such as `777` or `32S` names more than one type and is absent on
purpose: AirTrail accepts a wrong aircraft without a word, so a guess would be
invisible. An airport needs no table, because the save endpoint resolves an
airport by ICAO or by IATA.

## Testing notes

`internal/testutil.Golden(t, name, got)` compares `got` with
`testdata/<name>.golden`. A change to the output shows up as a change to a
golden file in the pull request diff. `internal/tripittest.New()` starts a
fake TripIt server; a test sets the response of a route with `SetFeed`,
`SetTrips`, `SetTripDetail` or `SetDownload` before it makes a request that
reads that route. A web API v2 request with `it_session_id` and no
`session_id` gets a new `session_id` and `RenewedSession(value)`, or `500`
for a value that `RejectSession` names. `Paths` returns each request path,
so a test can check which trips a run read. The download route returns `403` when the request holds
no `Referer` header, the way TripIt rejects a request with no browser
header, and for a UUID that `BlockDownload` names.

No test fixture holds a real feed URL, a real cookie, a real name, or a real
trip. Each fixture is synthetic, built to the shape of the operator feed
described in [docs/research.md](docs/research.md).

`internal/airtrailtest.New()` starts a fake AirTrail instance that holds the
saved flights in memory; `RejectKey` and `FailFlight` make it answer `401`
and `400`, and `Flights` and `Counts` let a test prove that an unchanged run
sends nothing.

A scenario test drives `archive.Run` with an injected clock, so no test
reads the real time. `internal/ics` also carries a fuzz target for the
reader-writer round trip and for the line-folding rule. A test with the
`live` build tag reads `TRIPIT_FEED_URL` from the real environment and skips
when it is empty; CI never sets that build tag.

## CI/CD

`.github/workflows/ci.yml` runs `lint` and `test`, then `build-push`, then
`security-scan`, then `release`. A pull request stops after `build-push`: it
pushes no image, so it loads the image into the local daemon and runs
`tripit-exporter version` as the smoke test. The distroless image has no shell,
so `version` is the only smoke test that the image can run.

Each build carries the tag `sha-<commit>` and no version tag.
`release-image-tags.yml` applies the version tags to that same digest when a
person publishes the release. `latest` therefore never moves before the release
notes exist. A `v*.*.*` tag makes a draft release that holds the notes
template, the generated sections, and the `image-digest.txt` asset. A person
writes the highlights and publishes the draft.

Each pull request carries exactly one `changelog:` label.
`pr-label-validation.yml` fails the pull request without it, and
`.github/release.yml` maps the label to a section of the release notes.
