# Implementation plan

This plan builds the design in [research.md](research.md). Read that file for the facts about TripIt. This file states the order of the work, the files that each phase adds, and the test that closes each phase.

## Recommendation

Build the project in six phases, and put the CI and the test harness before the first feature:

| Phase | Result | Release |
|---|---|---|
| 0 | The GitHub repository, the README, CLAUDE.md, the branch rules, and harness | None |
| 1 | CI, the release pipeline, and the test harness, around a binary that only prints its version | `v0.0.1`, a pre-release to test the pipeline |
| 2 | The scheduled feed run and the archive | `v0.1.0` |
| 3 | The one-time backfill | `v0.2.0` |
| 4 | Events made from the v2 objects. Do this phase only if phase 3 finds that TripIt blocks the download | `v0.2.x` |
| 5 | The scheduled v2 refresh, an option that is off by default | `v0.3.0` |

The reasons:

1. Each feature pull request after phase 1 gets lint, tests, a coverage gate, a `changelog:` label check, and an `agent-review`. No feature merges before the gates exist.
2. The archive merge deletes data. The fake TripIt server and the scenario tests exist before the first line of merge code, so each delete rule has a test from the start.
3. The feed run is useful by itself, because the archive starts to grow past 90 days from its first run. The backfill can come later with no loss.

The fact against it: phase 1 releases an image that does nothing useful. Its only purpose is to prove the pipeline, and it costs one day before any TripIt code exists.

## Phase 0: the repository, the documents, and harness

### The GitHub repository

1. Create `aaronspruit/tripit-exporter` as a public repository with the MIT license, the same as garmin-activities-download. A ruleset on a public repository costs nothing. No secret goes into the repository, because every test fixture is synthetic.
2. Push one bootstrap commit to `main`: `.gitignore`, `LICENSE`, `go.mod`, `README.md`, `CLAUDE.md`, `docs/research.md` and `docs/plan.md`. This is the only direct push to `main`.
3. Create a ruleset named `default` on `main`. Require a pull request, set **Require approvals** to 1, and add **Repository admin** to the bypass list. Condition 5 of the harness contract needs this, because it stops an agent from merging its own pull request.
4. Create the ten `changelog:` labels with the same names, colors and descriptions as garmin-activities-download. `gh label clone aaronspruit/garmin-activities-download` copies them. It also copies the `agent-` labels, and phase 0 needs those too.
5. Open one GitHub issue for each phase, and paste the section of this plan into it. Rule 6 of the harness rules needs an issue to hold each decision.

### README.md

The README is for a person who runs the container. It describes the current behavior only. Breaking changes go in the pull request and the release notes. Each phase updates the README in the same pull request as the code.

| Section | Content | Phase |
|---|---|---|
| Summary | Two sentences: what the tool writes, and from which TripIt sources | 0 |
| Status | One line that names the current phase. Delete the section at `v0.2.0` | 0 |
| Set up the calendar feed | Turn on "Display individual plans within a trip", and copy the private feed URL | 2 |
| Run with Docker Compose | `.env`, `compose.yaml`, and `docker compose run --rm tripit-exporter` | 2 |
| Run on Kubernetes | `k8s/cronjob.yaml`, and how to store the feed URL as a secret | 2 |
| Run from a host crontab | The static binary and one crontab line | 2 |
| Configuration | A table of each environment variable, its default, and the `/run/secrets` name | 2 |
| Output | The `data/` tree, the fields of a trip JSON file, and the merge and delete rules in short form | 2 |
| Exit codes | `0`, `1` and `2`, and what the operator does for each | 2 |
| Backfill | How to copy the session cookie from the browser, and `docker compose run --rm -it tripit-exporter backfill` | 3 |
| Limits | The 90-day feed window, free-text detail in the feed, and the structured fields from the backfill only | 2 |
| Development | `go test ./...`, the golden files, and a link to CLAUDE.md | 1 |

### CLAUDE.md

CLAUDE.md is for an agent that changes the code. It holds the rules block, the commands, and each constraint that the code cannot state. It does not repeat the README. Follow the layout of the garmin-activities-download CLAUDE.md:

| Section | Content | Phase |
|---|---|---|
| Mandatory rules | The two `harness:rules` markers. `check-repo.sh --sync-rules` fills them | 0 |
| Commands | `go vet`, `go test -race`, golangci-lint, the golden file update flag, a fuzz run, and `docker build` | 1 |
| Architecture | One paragraph for each package, and the exit code precedence | 2 |
| The archive | The merge rules, the `in_feed` flag, the `DTSTAMP` rule, and atomic writes. Say why each rule exists | 2 |
| The backfill | The headers, the pace, the resume rule, and why the cookie is never saved | 3 |
| Testing notes | The fake server, the fixtures, the scenario tests, and the rule that no fixture holds real data | 1 |
| CI/CD | The job order, the image tags, the draft release, and the labels. Copy the garmin text, and change the lint and test steps | 1 |

### harness

Do these steps in the harness repository. [monitored-repo.md](https://github.com/aaronspruit/harness/blob/main/docs/monitored-repo.md) states the eight conditions and the reason for each.

1. Install the GitHub App on `tripit-exporter`. Give it the four permissions and the four event subscriptions of condition 2.
2. Add this entry to `repos` in `repos.yaml`. The project has tests and makes releases, so it runs the same nine workflows as garmin-activities-download:

   ```yaml
   - owner: aaronspruit
     name: tripit-exporter
     workflows:
       - agent-research
       - agent-plan
       - agent-triage
       - agent-fix
       - agent-docs
       - agent-test
       - agent-review
       - agent-review-reply
       - agent-release-notes
   ```

3. Run `scripts/check-repo.sh --apply tripit-exporter`. It creates the `agent-` labels. The memory project `tripit-exporter` exists already.
4. Run `scripts/check-repo.sh --sync-rules tripit-exporter`. It opens a draft pull request that fills the rules block in CLAUDE.md. Merge it.
5. Run `scripts/render-manifests.sh`, then apply `manifests/generated/`. Without this step, no event starts a task.
6. Run `scripts/check-repo.sh tripit-exporter` again. Make sure that it exits with `0`.

Add a `.harness.yml` file to the root of tripit-exporter:

```yaml
context: |
  Run `go vet ./...`, `go test -race ./...` and `golangci-lint run` before you open a pull request.
  No test fixture holds a real feed URL, a real cookie, or a real trip.
```

### The dev container

The distroless image has no shell, so the dev container cannot use the Dockerfile the way garmin-activities-download does. Use the `mcr.microsoft.com/devcontainers/go` image instead. Copy these parts of the garmin dev container: the host home mount, `link-claude-home.sh`, the `workspaceMount` at the host path, and the `github-cli`, `node` and `claude-code` features. Install golangci-lint in `postCreateCommand`.

### Phase 0 is complete when

1. `scripts/check-repo.sh tripit-exporter` exits with `0`.
2. A test issue with the `agent-triage` label gets a comment from harness.

## Phase 1: CI, the release pipeline, and the test harness

### The binary

Add the smallest program that the pipeline can build, scan and run:

```text
cmd/tripit-exporter/main.go    os.Exit(run(os.Args[1:], env, stdin, stdout, stderr))
cmd/tripit-exporter/run.go     parses the subcommand and returns the exit code
```

`tripit-exporter version` prints the version and exits with `0`. The build sets the version with `-ldflags "-X main.version=..."`. The CI smoke test runs this command, because the distroless image has no shell and no `python -c` equivalent.

`run` takes its environment, input and output as arguments, and it never calls `os.Exit`. The tests call `run` directly, so a test of an exit code needs no subprocess.

### The Dockerfile

```text
FROM golang:<version> AS build     CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION"
FROM gcr.io/distroless/static:nonroot
COPY --from=build /tripit-exporter /tripit-exporter
ENTRYPOINT ["/tripit-exporter"]
```

Pin both base images by digest. Dependabot updates the digests. The `nonroot` image runs as UID 65532. `compose.yaml` and `k8s/cronjob.yaml` state that UID, as the garmin files state UID 1000.

Trivy reads the Go version from the binary, and it reports the CVEs of the Go standard library. A failed scan therefore means that the Go toolchain needs an update. The `go` line in `go.mod` holds the toolchain version, and CI reads it from there.

### The CI files

Copy these files from garmin-activities-download. Change only the rows in the table below:

| File | Change |
|---|---|
| `.github/workflows/ci.yml` | Replace `lint` and `test`. Change the smoke test to `version`. The `build-push`, `security-scan` and `release` jobs stay the same |
| `.github/workflows/pr-label-validation.yml` | None |
| `.github/workflows/release-image-tags.yml` | None |
| `.github/release.yml` | None |
| `.github/release-notes-template.md` | Replace the two kinds of breaking change. Write them for this project: a renamed or removed environment variable, and a change to the `data/` layout or to the trip JSON schema |
| `.github/dependabot.yml` | Replace `pip` with `gomod`. Keep `github-actions` and `docker`. Every entry keeps the `changelog:dependencies` label |
| `.dockerignore` | Replace the Python entries with `*_test.go`, `testdata/` and `data/` |

The `lint` job:

1. `actions/setup-go` with `go-version-file: go.mod`.
2. `test -z "$(gofmt -l .)"`.
3. `go vet ./...`.
4. `golangci/golangci-lint-action`, with a `.golangci.yml` in the version 2 format.

The `test` job:

1. `go test -race -coverprofile=coverage.out ./...`.
2. Stop the job if the total from `go tool cover -func` is less than 80%.
3. Run each fuzz target for 15 seconds. `go test -fuzz` takes one target at a time, so a loop in the step runs each one.
4. Upload `coverage.out` as an artifact.

After the first green run, add `lint`, `test`, `build-push` and `validate-changelog-label` as required status checks in the ruleset.

### The test harness

The harness has five parts. Phase 1 builds the first three. Phases 2 and 3 add to all five.

1. The `run` entry point. A test calls `run` with a map of environment variables, a temporary `OUTPUT_DIR`, and a buffer for each output stream. It then compares the exit code, the output and the files on disk.
2. Golden files. `internal/testutil` holds `Golden(t, name, got)`. It compares `got` with `testdata/<name>.golden`. The flag `-update` writes the file instead. A reviewer reads a change to the output as a change to a golden file in the pull request.
3. The fake TripIt server. `internal/tripittest` starts an `httptest.Server` that serves the routes of [research.md](research.md). A test sets the response of each route for each request. Phase 1 adds the server with the feed route only.
4. Scenario tests (phase 2). A scenario is a list of steps. Each step sets the clock, sets the feed that the fake server returns, runs `run`, and compares the archive with a golden directory. The clock is an argument of `run`, and no test reads the real time.
5. Fuzz targets (phase 2). The ICS reader and writer get fuzz targets with a seed corpus in `testdata/fuzz/`.

The fixtures follow three rules:

1. Each fixture is synthetic. It copies the shape of the operator feed of 2026-09-14: LF line ends, no `VTIMEZONE`, an all-day trip event, and plan events in UTC. It holds no real name, trip, address, key or cookie.
2. A test that needs the real account has the build tag `live`. It reads `TRIPIT_FEED_URL` from the environment and skips when the variable is empty. CI never runs it. Phase 2 uses it to answer open questions 2 and 3 of the research.
3. A new fact about the TripIt format gets a fixture and a test in the same pull request as the code that uses the fact.

### Phase 1 is complete when

1. A pull request with no `changelog:` label fails `validate-changelog-label`.
2. A test pull request that lowers the coverage under 80% fails `test`.
3. The tag `v0.0.1` makes a draft release with the `image-digest.txt` asset and generated notes.
4. You publish that draft as a pre-release, and the image gets the tag `0.0.1-rc1` and no `latest` tag.
5. `docker run --rm ghcr.io/aaronspruit/tripit-exporter:0.0.1-rc1 version` prints `0.0.1`.

## Phase 2: the scheduled feed run

### The packages

```text
internal/secret     reads /run/secrets/<name> first, then the environment variable
internal/ics        reads and writes ICS
internal/feed       fetches the feed, and maps each HTTP result to an error type
internal/archive    loads, merges and writes the trip files, and makes the two ICS files
```

All packages use the Go standard library only.

### internal/ics

1. The reader accepts LF and CRLF, and it joins folded lines.
2. The reader keeps each property as raw text, with its parameters and its order. The writer then writes an event with the same content that it read. The code parses only the properties that it uses: `UID`, `DTSTART`, `DTEND`, `DTSTAMP` and `DESCRIPTION`.
3. The writer writes CRLF, and it folds each line at 75 octets. It never splits a multi-byte UTF-8 character.

### internal/feed

The fetch has a timeout of 60 seconds. Each result maps to one exit code:

| Result | Exit code | Reason |
|---|---|---|
| `200` with a `VCALENDAR` that parses | `0` | Success |
| `401`, `403`, `404` or `410` | `1` | The feed URL is wrong or revoked. A person must copy a new one |
| `429` | `0` | A rate limit. The next run continues |
| `5xx`, a network error, or a body that does not parse | `2` | Any other error |

A body that does not start with `BEGIN:VCALENDAR` and end with `END:VCALENDAR` is a failed fetch. A failed fetch changes nothing in the archive.

The feed URL is a credential. An error from `net/http` includes the full URL, so the code replaces the URL in each error before it logs the error. A test makes sure that no output of a failed run holds the key.

### internal/archive

A trip file has this form:

```json
{
  "schema": 1,
  "uuid": "<trip uuid>",
  "trip_id": "<numeric trip id>",
  "start": "2026-06-15",
  "end": "2026-06-19",
  "in_feed": true,
  "events": [{"uid": "item-<uuid>@tripit.com", "ics": "BEGIN:VEVENT\r\n...END:VEVENT\r\n"}],
  "v2": null
}
```

The merge follows [the rules in research.md](research.md#calendar-feed). The plan adds these decisions:

1. The run compares an event with the archived event, and it ignores `DTSTAMP`. If only `DTSTAMP` is different, the run keeps the archived event. The file then stays the same, and the run writes nothing.
2. No field changes on a run that finds no change. A file holds no fetch time for this reason.
3. The delete rules apply only to a trip with `in_feed: true`. The backfill reads trips that other travelers share with you (`traveler=all`), and the feed possibly does not hold them. Without this flag, the next feed run deletes each shared trip inside the window.
4. The window margin is 7 days. A trip that is absent from the feed and ended 83 to 97 days before the fetch stays in the archive. The archive keeps a trip when the rule is not certain, because a kept trip is the error that a person can correct.
5. A plan event whose numeric trip ID has no trip event in the same fetch is not written. The run logs a warning and exits with `0`. The next fetch that holds the trip event adds the plan.
6. Events are in order of `DTSTART`, then `UID`. The two ICS files are in order of trip start, then trip UUID. The same archive therefore always makes the same bytes.
7. The run writes each file to a temporary file in the same folder, then calls `os.Rename`. It writes a file only when the SHA-256 of the new content is different from the file on disk.

### Deployment files

1. `compose.yaml` with `user: "65532:65532"` and the volume `./data:/data`.
2. `.env.example` with `TRIPIT_FEED_URL`. `OUTPUT_DIR` defaults to `/data`.
3. `k8s/cronjob.yaml`, copied from garmin-activities-download. It has a Secret for the feed URL and no token volume. The schedule is every 6 hours. TripIt refreshes the feed every 15 minutes, and a trip changes less often than that.

### Tests

| Test | Kind |
|---|---|
| The reader accepts LF and CRLF, and joins folded lines | Table |
| The writer folds at 75 octets, and never inside a UTF-8 character | Table and fuzz |
| Read, write, and read again gives the same events | Fuzz |
| Each HTTP result gives the exit code in the table | Table, with the fake server |
| No output of a failed run holds the feed key | Table |
| A second run with the same feed and a new `DTSTAMP` writes no file | Scenario |
| A plan that is absent from a trip in the feed is deleted | Scenario |
| A trip that ended more than 97 days ago and is absent stays | Scenario |
| A trip that ended less than 83 days ago and is absent is deleted | Scenario |
| A trip at the edge of the window stays | Scenario |
| A trip with `in_feed: false` that is absent stays | Scenario |
| A failed fetch after a good fetch changes nothing | Scenario |
| The run keeps the `v2` field of a trip when it rewrites the trip | Scenario |
| A crash before `os.Rename` leaves the old file whole | Unit, with a writer that fails |

### Phase 2 is complete when

1. All tests pass in CI, and the coverage is 80% or more.
2. The `live` test against the real feed passes on the operator machine. Its results answer open questions 2 and 3 of the research. Write the answers to research.md and to the phase 2 issue.
3. The CronJob ran in the cluster for one week. Each run exited with `0`, and a run with no trip change wrote no file.
4. You publish `v0.1.0`.

## Phase 3: the backfill

### Answer the open questions first

Before you write the client, answer open questions 1 and 4 of the research with the real account. Question 1 decides the header set of the download. Question 4 decides if the client must build the file name. Write the answers to research.md and to the phase 3 issue. If TripIt blocks the download from an address that is not the browser address, do phase 4 as part of this phase.

### The package

`internal/tripitweb` holds the client for web API v2 and the download URL.

1. The client sends the cookie, `Accept: application/json` and `X-Requested-With: XMLHttpRequest` to each API route.
2. The client sends the full browser header set to the download URL. The set is a fixed list in the code.
3. A helper turns a JSON value that is an object into an array of one. Each plan list goes through it.
4. The client removes a duplicate object by its `uuid`.
5. The client waits 5 seconds before each request after the first. Each request stops after 20 seconds. The client applies each `Set-Cookie` header to the cookie of its next request.
6. A `401` gets one retry after 5 seconds. A second `401` stops the run with exit code `1`.
7. A `429`, an HTTP/2 protocol error, a TCP reset, or a request that stops after 20 seconds gets a retry after 1, 2, 4, then 8 minutes. If TripIt still throttles the request, the run stops with exit code `0`, and the next run continues.

### The command

`tripit-exporter backfill`:

1. Prompts for the cookie. If the input is a terminal, the prompt turns off the echo and canonical mode with the Linux `ioctl` from the `syscall` package. Canonical mode cuts a line at 4095 bytes, and a full `Cookie` header can be longer. The cookie never goes to a file or to a log.
2. Calls `/api/v2/get/profile`. A `401` stops the run with exit code `1` before any trip request.
3. Lists all trips, with `past=true&traveler=all` and `past=false&traveler=true`.
4. Skips each trip whose file already holds `v2` and either events or `empty_download: true`. A second run therefore continues where the first run stopped, and tries a failed download again.
5. Reads the detail of each other trip, and stores the response in `v2` as it was read.
6. Downloads the ICS of a trip that has no events, and adds the events with `in_feed: false`. If the download returns a status other than `200` or `429`, or a calendar that does not parse, the run writes a warning, keeps `v2` with no events, and continues. A download that holds zero events sets `empty_download: true`.
7. Writes each trip file when it finishes that trip, and not at the end of the run.

The feed run sets `in_feed: true` on a backfilled trip when the feed holds the trip.

### Tests

The fake server gains the v2 routes and the download route. It returns `403` from the download route when a browser header is absent, as TripIt does.

| Test | Kind |
|---|---|
| A plan list with one object gives an array of one | Table |
| A plan in two shared trips is stored once | Table |
| Paging reads each page up to `max_page`, and waits the pace before each page after the first | Fake server |
| A `401` then a `200` succeeds. Two `401` responses exit with `1` | Fake server |
| A `429` in the middle exits with `0`, and the trips before it are on disk | Scenario |
| A second run skips the trips that have `v2` and events, or `v2` and an empty download | Scenario |
| A trip file with `v2`, no events and no `empty_download` key gets the download | Scenario |
| A blocked download, or a calendar that does not parse, keeps `v2`, the run continues, and the next run tries the download again | Scenario |
| A reset gets a retry after a wait. A reset or a stalled request on every retry exits with `0` | Fake server |
| A trip UUID that is not safe as a file name gets a warning and no file | Scenario |
| The download without the browser headers gets `403` | Fake server |
| A backfilled trip with `in_feed: false` stays after a feed run that does not hold it | Scenario |
| No output of any run holds the cookie | Table |

### Phase 3 is complete when

1. A backfill of the operator account finishes, in one run or in more.
2. A feed run after the backfill deletes no backfilled trip.
3. You publish `v0.2.0`.

## Phase 4: events from the v2 objects

Do this phase only if phase 3 finds that TripIt blocks the download. The backfill then makes each event of a trip from its v2 objects, with the `SUMMARY` and `DESCRIPTION` format of the feed. A flight event gets the `UID` from the `AirObject`. The v2 data has no `UID` for a check-in or a check-out event, so the backfill makes one from the lodging `uuid`. A later feed run then holds two events for the same stay inside the window. A merge rule for this case is a decision for this phase, and golden tests compare each made event with the export of the same trip.

### Findings from the phase 3 backfill

Do not start this phase while the download works. The phase 3 backfill of 2026-09-16 got the download for each trip, and a made event cannot keep the `UID` of the feed for half of the plan events. The one fact against this decision: a backfill with no download sends one request for each trip instead of two, so it takes half the time under the limit of about 50 requests in 10 minutes that [the research](research.md#open-questions) records.

The comparison used 70 trips, with 70 trip events and 421 plan events, and compared each downloaded event with the v2 objects of its trip.

| Event data | In the v2 objects |
|---|---|
| Trip event `UID` | Yes: the trip `uuid` |
| Flight event `UID` | Yes: `item-<Segment uuid>`, 215 of the 421 plan events |
| `UID` of a lodging, car or parking event | No. 206 of the 421 plan events: 75 check-in, 75 check-out, 27 car pick-up, 27 car drop-off, 1 parking arrival, 1 parking departure |
| Numeric trip ID in each `DESCRIPTION` link | No. A made link must use `/trip/show/uuid/<uuid>` |
| `GEO` | Not exactly. A flight event carries the coordinates of a city, and v2 holds only the airport coordinates. A lodging `GEO` has 7 decimal places, and the v2 `Address` has 6 |
| `SUMMARY`, `LOCATION` | Yes: the trip `display_name` and `primary_location`, the flight `<airline code><number> <from> to <to>`, and `Check-in: <lodging display_name>` |
| `DTSTART`, `DTEND` | Yes: `date`, `time` and `utc_offset`. A check-in or check-out event lasts one hour. The trip `DTEND` is the day after `end_date` |
| Gate, terminal, phone and traveler name in `DESCRIPTION` | Yes |

The v2 objects also hold data that no event holds: confirmation numbers, seats, cost, and the booking site.

No other v2 call gives the missing `UID` values. A TripIt ID has the form `<8 hex>-<4 hex>-9000-<kind>-<12 hex number>`, and a lodging, car or parking event `UID` is kind `0003`, a segment. The v2 objects hold no segment `uuid` for these plans, and the first part of the ID looks random, so the `UID` cannot be calculated from the object `uuid`. On 2026-09-17 a search of every response in the Network tab of the TripIt website, on a trip with a hotel and a car rental, did not find the check-in `UID` in any form. The mobile app API was not tested.

## Phase 5: the scheduled v2 refresh

The scheduled run can also read web API v2, so that the archive keeps the structured fields of each trip current. The refresh is off by default. It uses the "Keep me signed in" value `it_session_id`, and it keeps each new value that TripIt returns in a state file. [The research](research.md#login-and-session-lifetime) holds the facts that this design uses.

The reasons:

1. `it_session_id` alone makes a session, and each new session gives a new value with 15 more days. A daily run therefore keeps the session with no person, no password and no browser.
2. The newest value always has 15 days left. The state file works if TripIt rejects old values and also if it does not.
3. The refresh uses the client and the archive of phase 3. The image stays a static binary that uses only the standard library.

The fact against it: the state file holds a value that gives full access to the TripIt account, in the output folder. The one-time backfill keeps the cookie out of every file.

### Answer the open questions first

Before you write the code, answer [open question 3](research.md#open-questions) with the real account. The answer decides the setup steps in the README: if a sign-out stops the value, the person closes the browser and does not sign out. It also decides the status that the refresh reads as a rejected value. Open question 4 does not block this phase.

### The variables

| Variable | `/run/secrets` name | Default | Description |
|---|---|---|---|
| `TRIPIT_JSON_REFRESH` | | `false` | When `true`, the scheduled run does the refresh after the feed merge |
| `TRIPIT_JSON_REFRESH_DAYS` | | `7` | The refresh reads a trip again only when the trip ends on or after this number of days before the run, or has no end date |
| `TRIPIT_SESSION` | `tripit_session` | none | The first `it_session_id` value. The refresh reads it only when the state file is absent or rejected |

A user does not know the TripIt API version, so the variable names and the README do not say "v2". They say "JSON", to show that the refresh updates the structured fields of each trip file, and not the ICS files.

### The session

1. The refresh reads the value from `<OUTPUT_DIR>/.tripit-session` first, then from `TRIPIT_SESSION`. It skips an empty value, and a value that is the same as the one before.
2. For each value, the client sends `it_session_id=<value>` to `/api/v2/get/profile`. A rejected value goes to the next value. When no value remains, the run exits with `1`.
3. After the profile request, and at the end of the run, the refresh writes the current `it_session_id` of the client to the state file with mode `0600`. It writes only when the value changed, with a temporary file and `os.Rename`, as the archive does.
4. `tripitweb.Client` gets `CookieValue(name string) string`, which returns the current value of one cookie. The package writes no cookie to a file.
5. No log line, error message or archive file holds the value. With `TRIPIT_VERBOSE=true`, the lines hide it, as they hide the backfill cookie.

### The trips

The refresh lists all trips, as the backfill does. It reads the detail of a trip when one of these is true:

1. The archive holds no `v2` object for the trip. The refresh then does the full backfill of that trip, with the download, at any age.
2. The trip holds `v2`, no events and no `empty_download: true`. The refresh tries the download again.
3. The `end` of the trip is on or after the run date minus `TRIPIT_JSON_REFRESH_DAYS` days, or the trip has no `end`.

The `last_modified` of a trip does not show a change to its plans, so the refresh reads the detail of each trip in the window. The refresh does not download the events of a trip that holds events, because the feed holds the events of each trip that ended in the last 83 days. A window of more than 83 days refreshes `v2` alone for the older trips.

The refresh keeps the stored `v2` object when the new response differs from it only in the top-level `timestamp`. The archive therefore changes only when TripIt data changes. The refresh writes each trip file when it finishes that trip.

### The run

1. When `TRIPIT_JSON_REFRESH` is `true` and no state file and no `TRIPIT_SESSION` exist, the run exits with `2` before any request. A value of `TRIPIT_JSON_REFRESH_DAYS` that is not a whole number of `0` or more also exits with `2`.
2. A feed error exits with its code, and the refresh does not run.
3. After the feed merge, a refresh error sets the exit code: a rejected session gives `1`, a `*RateLimitedError` gives `0`, and each other error gives `2`.
4. `k8s/cronjob.yaml` gets the optional `TRIPIT_SESSION` key in its Secret, `TRIPIT_JSON_REFRESH` in a comment, and an `activeDeadlineSeconds` of `1800`, because a throttled request can wait 15 minutes. `.env.example` gets the three variables in comments.

### Tests

The fake server gains the session route behavior: a request with `it_session_id` and no `session_id` gets a new `session_id` and a new `it_session_id`, and a value that the test names gets the rejected status.

| Test | Kind |
|---|---|
| With `TRIPIT_JSON_REFRESH` absent, the run sends no v2 request | Scenario |
| With the refresh on and no value, or a bad `TRIPIT_JSON_REFRESH_DAYS`, the run exits with `2` and sends no request | Table |
| The state file value comes before `TRIPIT_SESSION`, and the new value goes to the state file with mode `0600` | Scenario |
| A rejected state file value, then an accepted `TRIPIT_SESSION`, replaces the state file | Scenario |
| Two rejected values exit with `1`, and the feed merge stays on disk | Scenario |
| A trip that ended before the window gets no request. A trip in the window, a future trip, and a trip with no `end` get the detail request | Scenario |
| A trip older than the window with no `v2` gets the full backfill | Scenario |
| A detail that differs only in `timestamp` writes no file | Scenario |
| A throttle on each retry exits with `0`, and the trips before it are on disk | Scenario |
| No output of any run holds the session value, and the state file is the only file that holds it | Table |

### Phase 5 is complete when

1. The operator turns on the refresh on the real deployment. A second run on the same day writes no trip file.
2. A run on a later day passes with the value from the state file alone.
3. You publish `v0.3.0`.

## Out of scope

1. A login with the email and password. Akamai blocks it without a browser that hides its automation ([research](research.md#login-and-session-lifetime)).
2. The official API v1.
3. The import of the archive into AirTrail. That work belongs to homek8.
4. An image for more than one CPU architecture.
