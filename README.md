# tripit-exporter

tripit-exporter keeps a local archive of your TripIt trips: one JSON file for each trip, one ICS file for each trip, and one ICS file for all trips. A scheduled run reads the TripIt private calendar feed, and a one-time backfill reads the TripIt web API to add the older trips.

## Status

Phase 1 of [the plan](docs/plan.md): CI, the release pipeline, and the test harness. The `tripit-exporter` binary only prints its version; it does not yet read a TripIt feed.

## Development

```bash
go test ./...
```

Golden files under each package's `testdata/` hold expected output. Run `go test ./... -update` to write them instead of comparing against them, then review the diff. See [CLAUDE.md](CLAUDE.md) for the full command list and the CI checks a pull request must pass.
