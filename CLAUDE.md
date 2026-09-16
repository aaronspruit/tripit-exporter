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
the feed and updates the archive; `version` prints the version; `backfill`
runs the one-time backfill.

| Package | Holds |
|---|---|
| `internal/secret` | Reads a credential from `/run/secrets/<name>`, then from an environment variable |
| `internal/ics` | Reads and writes the VEVENT blocks of an ICS calendar, keeping each property as raw text |
| `internal/feed` | Fetches the calendar feed, and maps each HTTP result to an error type |
| `internal/archive` | Loads, merges and writes the trip files, and makes the two ICS files |
| `internal/tripitweb` | Calls the TripIt web API v2 and the trip download URL with a session cookie |

The exit code order: a feed fetch error takes the code from
[the table](README.md#exit-codes) before the archive runs at all. An archive
error, for example a disk failure, exits with `2`.

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
request. A web API v2 route also gets `Accept: application/json` and
`X-Requested-With: XMLHttpRequest`; the download URL gets a fixed set of
browser headers instead, because TripIt returns `403` to a request without
them. `docs/research.md` open questions 1 and 2 have not run against the
real account yet, so the exact header set and the download file name are
best-effort, not confirmed; an operator must run the backfill against the
real account and update `docs/research.md` and the phase 3 issue with what
it finds.

The client waits 1 second between two trips (`Client.Pace`), and retries a
`401` once after 5 seconds; a second `401` becomes an `*AuthError`, and a
`429` or an HTTP/2 protocol error becomes a `*RateLimitedError`. `cmd`
turns the first into exit code `1` and the second into exit code `0`.

`cmd/tripit-exporter backfill` writes each trip file as soon as it finishes
that trip, and skips a trip whose archived file already holds a `v2`
object and events, so a second run resumes where the first stopped. A
download status other than `200` or `429` becomes a
`*tripitweb.DownloadError`: the run writes a warning, keeps the `v2`
object of that trip with no events, and continues. A trip UUID that
`archive.ValidTripUUID` rejects gets a warning and no request. The backfill
prompts for the cookie on stdin and, when stdin is a terminal, turns off the echo with the
Linux ioctl in `cmd/tripit-exporter/terminal_linux.go`. The cookie exists
only in that prompt and in `Client.Cookie`; it never reaches a file, a log
line, or an error message.

`TRIPIT_WEB_BASE_URL` replaces the TripIt host that `internal/tripitweb`
calls. It is empty in production; a test sets it to a fake server's URL.

## Testing notes

`internal/testutil.Golden(t, name, got)` compares `got` with
`testdata/<name>.golden`. A change to the output shows up as a change to a
golden file in the pull request diff. `internal/tripittest.New()` starts a
fake TripIt server; a test sets the response of a route with `SetFeed`,
`SetTrips`, `SetTripDetail` or `SetDownload` before it makes a request that
reads that route. The download route returns `403` when the request holds
no `Referer` header, the way TripIt rejects a request with no browser
header, and for a UUID that `BlockDownload` names.

No test fixture holds a real feed URL, a real cookie, a real name, or a real
trip. Each fixture is synthetic, built to the shape of the operator feed
described in [docs/research.md](docs/research.md).

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
