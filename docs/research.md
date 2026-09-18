# TripIt export research

Research only. Nothing is built. The sources were read on 2026-09-14.

## Recommendation

Build the scheduled run on the private calendar feed, and keep an archive that holds each event after it leaves the feed. Then add a one-time backfill that reads the TripIt web API (v2) with a browser session. The backfill gets the trips that are older than the feed window, with every field of each trip.

The reasons:

1. The feed needs no login. The secret URL is the only credential, so a scheduled run never needs a person.
2. An archive that merges the events by `UID` grows past 90 days, from the first run forward.
3. The backfill fills the history before the first run. The web API is the only path that returns every trip with every field. The official API is closed to new applications, and basic auth needs a support request.
4. A person runs the backfill one time with a pasted cookie. A scheduled refresh of web API v2 is an option: the "Keep me signed in" value `it_session_id` gets 15 more days with each new session ([Login and session lifetime](#login-and-session-lifetime)).

The fact against it: the scheduled run keeps only what the feed gives. A plan in the feed has its detail as free text, and the structured fields come from the backfill only.

## The model: garmin-activities-download

Copy these parts of [aaronspruit/garmin-activities-download](https://github.com/aaronspruit/garmin-activities-download):

1. The container runs one time and exits. The schedule lives in `k8s/cronjob.yaml` or in the host crontab.
2. A command that needs a person runs one time with `docker compose run --rm -it`. The scheduled run has no prompts.
3. A secret is read from `/run/secrets/<name>` first, then from the environment variable.
4. Exit codes: `0` success, `1` a credential that a person must renew, `2` any other error. A rate limit stops the run early with `0`, and the next run continues.
5. `compose.yaml`, the image release workflow, and the `changelog:` labels. The lint and test steps of CI change to `go vet`, `go test` and golangci-lint.

Do not copy the marker files. A Garmin activity does not change after the upload, so one marker for each file is enough. A trip changes until the day of travel and sometimes after. Write a file when its content hash changes, and replace it with a temporary file and `os.Rename`.

## Data sources

| Source | History | Detail | Credential | Automatic | Status |
|---|---|---|---|---|---|
| Calendar feed | Last 90 days and all future trips | ICS events. The detail is free text | Secret URL | Yes | Documented |
| Web API v2 | All trips | Structured JSON for each plan | Session cookie | Yes, while the session is valid | Undocumented |
| "Export trip to calendar" | One trip, any date | The same ICS events as the feed | Session cookie | Yes, with the headers of a browser | Undocumented |
| API v1 with OAuth | All trips | Structured JSON | Consumer key and OAuth token | Yes | Closed to new integrations |
| API v1 with basic auth | All trips | Structured JSON | Email and password | Yes | Off by default |

### Calendar feed

The URL has the form `https://www.tripit.com/feed/ical/private/<key>/tripit.ics`. The key in the URL is the credential, so store the whole URL as a secret.

The feed holds the trips of the last 90 days and all future trips. TripIt gives performance as the reason, and no setting changes the window.

The feed holds one event for each trip by default. To get one event for each plan, set "Display individual plans within a trip" in the Calendar Feed configuration on the website. The TripIt app calls the same setting "Include detailed items".

The facts below come from the operator feed on 2026-09-14. It held one trip with two flights and one hotel, and the setting for individual plans was on.

1. `curl` with no browser headers got `200`. The response has `Cache-Control: max-age=900`, and the file has `X-PUBLISHED-TTL:PT15M`. A schedule faster than 15 minutes gets the same file.
2. The lines end with LF, not with the CRLF that RFC 5545 requires. The parser must accept both.
3. The file has no `VTIMEZONE`. A plan event gives `DTSTART` and `DTEND` in UTC. The trip event is all-day (`VALUE=DATE`), and its `DTEND` is the day after the last day.
4. `DTSTAMP` is the time of the fetch. Two fetches were the same except for `DTSTAMP`. The content hash must ignore `DTSTAMP`, or each run writes every file again.
5. The window rolls from the day of the fetch. The sample trip started 91 days and ended 88 days before the fetch, and the feed held the trip with all of its plans. The window therefore counts from the end of a trip, and a plan older than 90 days stays while its trip is inside the window.

| Event | `UID` | `SUMMARY` | `DESCRIPTION` |
|---|---|---|---|
| Trip | `<uuid>@tripit.com` | `<city>, <state>, <month> <year>` | The traveler name, the city, the dates, and a link to `https://www.tripit.com/trip/show?id=<trip id>` |
| Flight | `item-<uuid>@tripit.com` | `AS123 SEA to LAX` | `[Flight]`, the local departure and arrival times with the zone name, the airline name, the flight number, the terminals and the gates |
| Hotel | `item-<uuid>@tripit.com` | `Check-in: <hotel>` or `Check-out: <hotel>` | `[Lodging]`, the local time, the check-in or check-out time, the address and the phone number |

A hotel stay is two events of one hour, one at check-in and one at check-out. No event covers the nights. Each plan `DESCRIPTION` starts with a link to `https://www.tripit.com/trip/show/id/<trip id>`, so the archive can group the plans by trip. The feed holds no confirmation number, seat, cost, booking site or traveler list, not even as text in `DESCRIPTION`. The operator tested this on 2026-09-16 with a flight that holds an itinerary number, a cost and a seat in TripIt.

The `UID` of an event stays the same when a person edits it in TripIt. On 2026-09-16 the operator changed a trip name, and the flight number and departure time of a plan. Each event kept its `UID`, and only `SUMMARY`, `DTSTART` and `DTSTAMP` changed.

The archive keeps each `UID` with its latest `VEVENT`. A successful fetch replaces each event that it holds. The archive groups the events by the trip `uuid`, which is also the key of the v2 API. The trip event gives the `uuid` in its `UID` and the numeric trip ID in its link. A plan event links the numeric trip ID only, so the run finds the `uuid` through the trip event of the same fetch. If a trip is in a successful fetch and one of its archived events is absent, TripIt deleted that plan, so the archive deletes the event too. If a whole trip is absent and it ended more than 90 days before the fetch, the trip left the window, and the archive keeps it. If a whole trip is absent and it ended inside the window, TripIt deleted the trip, and the archive deletes its events. Keep a margin of some days at the edge of the window, because one sample does not give the exact boundary day. A failed fetch changes nothing.

JSON made from the feed holds the ICS fields and the `DESCRIPTION` text. Structured JSON needs the web API.

### Web API v2

The TripIt website calls these endpoints. Three public export tools use them ([caseyg/caseys-claude](https://github.com/caseyg/caseys-claude/blob/main/skills/tripit-export/SKILL.md), [DevSecNinja/ai-toolkit](https://github.com/DevSecNinja/ai-toolkit/blob/main/.apm/skills/tripit-exporter/SKILL.md), and the homek8 AirTrail import).

```text
GET https://www.tripit.com/api/v2/list/trip?exclude_types=weather&page_size=50&past=false&traveler=true&page_num=1
GET https://www.tripit.com/api/v2/list/trip?exclude_types=weather&page_size=50&past=true&traveler=all&page_num=1
GET https://www.tripit.com/api/v2/get/trip/uuid/<uuid>/include_objects/true?exclude_types=weather
GET https://www.tripit.com/api/v2/get/profile
```

Send the session cookie and these two headers with each request. Without `X-Requested-With`, the API returns `401` for a valid session.

```text
Accept: application/json
X-Requested-With: XMLHttpRequest
```

The list response holds `page_num`, `page_size`, `max_page` and `Trip`. The detail response holds `Trip` and one array for each plan type: `AirObject`, `LodgingObject`, `CarObject`, `RailObject`, `ActivityObject`, `NoteObject`, `MapObject`, `DirectionsObject`, `TransportObject`, `CruiseObject`, `RestaurantObject` and `ParkingObject`. The [API v1 documentation](https://tripit.github.io/api/doc/v1/) describes the fields of each type. The v2 responses for the operator account on 2026-09-14 gave these facts:

1. Each value is a string, also a number or a flag. An XML attribute arrives as an `@attributes` key.
2. A trip has `uuid`, `relative_url` and `last_modified`, and no numeric `id`. Its `uuid` is the `UID` of the trip event in the feed, without `@tripit.com`.
3. The `UID` values of both flight events in the feed are inside the `AirObject`. The `UID` values of the check-in and check-out events are not inside the `LodgingObject`.
4. Each object and each flight segment has `last_modified`.
5. The objects hold fields that the sample feed did not show: `supplier_conf_num`, `booking_site_conf_num`, `total_cost`, `seats`, `service_class`, `aircraft`, the lodging `notes`, and a `Traveler` with `ticket_num` and `frequent_traveler_num`.

The constraints:

1. If a list holds one item, the JSON gives an object and not an array. The code must normalize every list.
2. `traveler=all` also returns the trips that other travelers share with you. In the homek8 import, 39 of 514 flights were copies of the same flight in two trips. Deduplicate by object `uuid`.
3. TripIt publishes no rate limit. In the homek8 run, one `401` came after 31 trips and a retry after 5 seconds passed. TripRip reports `ERR_HTTP2_PROTOCOL_ERROR` after about 100 trips. caseyg waits 500 ms between two trips. Use a slow pace, and skip each trip that already has a file. A second backfill run then continues where the first run stopped.

The login form at `https://www.tripit.com/account/login` takes an email and a password, or a Google or Apple account. Two-factor authentication is optional. "Keep me signed in" works with a TripIt password only.

The backfill takes a pasted cookie. The operator logs in with a browser, copies the session cookie from the developer tools, and pastes it at the backfill prompt. The backfill does not save the cookie, and the image needs no browser. Each response from `www.tripit.com` sets the Akamai Bot Manager cookies `_abck` and `bm_sz`. On 2026-09-14, `curl` on the operator machine sent the pasted cookie with the `Accept` and `X-Requested-With` headers and no other headers. `/api/v2/get/profile` returned `200 application/json`. The download URL of "Export trip to calendar" returned `403 text/html` for the same cookie. The same download returned `200 text/calendar; charset=utf-8` when `curl` sent all the headers of the Firefox request ("Copy as cURL"). The block therefore depends on the request headers, and not on the TLS connection of `curl`. The cookie with the Firefox `User-Agent` alone still returned `403`. The machine had the same public address as the browser. If the API returns `401` after a retry, the session expired, and the backfill exits with `1`.

### Login and session lifetime

No program can log in with the email and password. On 2026-09-17, a Go client with the standard library loaded the login page, and then sent the form with its `csrf_token` and the Firefox headers. Akamai returned `403` "Access Denied" to the POST. It returned the same `403` for a fake account, so the block comes before TripIt reads the credentials. An unmodified headless Chromium 153 (Playwright, both `chrome-headless-shell` and the full Chromium) got the same `403`. The `_abck` cookie stayed "not validated" (`~-1~`) in each test. A browser that passes Akamai must hide its automation.

"Keep me signed in" sets `it_session_id` on `www.tripit.com`, with an expiry 15 days after the login. `session_id` ends when the browser closes. The other cookies are Akamai, consent, analytics and CSRF cookies. On 2026-09-17, the operator sent `it_session_id` alone to `/api/v2/get/profile`, with the API headers:

| Request | Status | Cookies that TripIt set |
|---|---|---|
| `it_session_id` only | `200 application/json` | a new `session_id`; a new `it_session_id` value, which expires 15 days after this request; `it_rec_br` and `it_rmd`, 365 days |
| The full browser `Cookie` header | `200 application/json` | no new `it_session_id` |
| The same `it_session_id` value again, 2.5 minutes later, after TripIt replaced it | `200 application/json` | one more new `it_session_id` value |

Thus `it_session_id` alone makes a session, and each new session gives a new value with 15 more days. An old value stays valid after TripIt replaces it, at least for some minutes. A fake `it_session_id` value gets `500`.

A sign-out does not stop `it_session_id`. On 2026-09-17, the operator logged in with "Keep me signed in" in a private Firefox window, copied the `Cookie` header, and signed out. 5 to 10 minutes later, `it_session_id` alone got `200` and a new value. The full header also got a new `session_id` and a new `it_session_id`, so the sign-out stopped `session_id` only.

The v2 detail response holds `timestamp`, which changes with each request. In the operator archive, 59 of the 70 trips that hold plan objects have an object with a `last_modified` that is newer than the `last_modified` of the trip, by a median of 530 days. Thus the `last_modified` of a trip does not show a change to its plans, and only the detail of a trip shows a change.

### Export trip to calendar

The website alone has this action, on the Trips page under "More Options". It downloads one static `.ics` file for one trip, at any age. The browser sends the session cookie to this URL:

```text
GET https://www.tripit.com/trip/download/uuid/<trip uuid>/tripit_Jun15_to_Jun18.ics
```

The operator exported the June 2026 trip on 2026-09-14 and compared it with the feed. The events were the same as the feed events: the same `UID`, `SUMMARY`, `DESCRIPTION`, times and `GEO`. Only the calendar header was different. The export adds `METHOD:PUBLISH`, puts the trip name in `X-WR-CALNAME` and `X-WR-CALDESC`, and has no `X-PUBLISHED-TTL`.

The backfill therefore downloads the events of each trip from this URL, and it makes no events of its own. This also gives the check-in and check-out events, whose `UID` values are not in the v2 objects. The URL accepts a client that is not a browser only when it sends the headers of a browser (read [Web API v2](#web-api-v2)), so the backfill sends them. If TripIt blocks the download, the backfill keeps the v2 objects of the trip with no events, and phase 4 of [the plan](plan.md#phase-4-events-from-the-v2-objects) makes the events from them.

### API v1

The [official API](https://tripit.github.io/api/doc/v1/) at `https://api.tripit.com/v1/` uses OAuth 1.0a with HMAC-SHA1. TripIt [closed it to new integrations](https://help.tripit.com/en/support/solutions/articles/103000391296-tripit-public-api). Connections that exist continue to work.

The documentation also describes basic auth with the email and password. It is off for each user by default, only TripIt support can turn it on, and the documentation limits it to testing. It is not on for the operator account (tested 2026-09-14). A request with no password returns `400 Request is missing password parameter.` for every account, so a `400` does not show whether basic auth is on.
## Proposed design

Go, with the standard library only. No TripIt client library is necessary, because the work is HTTP, JSON, SHA-256 hashes and a small ICS reader and writer. The ICS code accepts LF and CRLF, joins folded lines, and writes CRLF lines folded at 75 octets, as RFC 5545 requires.

The build makes one static binary with `CGO_ENABLED=0`. The image is `gcr.io/distroless/static` with that binary alone, so the image holds no OS packages and no package manager for a scanner to report. Do not use `scratch`, because the binary needs the CA certificates for HTTPS. A host crontab can run the same binary with no Docker.

| Variable | Description |
|---|---|
| `TRIPIT_FEED_URL` | The private feed URL. It is a secret |
| `OUTPUT_DIR` | Folder for the output files, `/data` by default |

`tripit-exporter` with no argument is the scheduled run, and it reads the feed. `tripit-exporter backfill` is the one-time backfill, and it prompts for the cookie.

```text
data/
├── tripit.ics              the events of every trip in the archive
└── trips/
    ├── <trip uuid>.json    one trip: its events, and its v2 objects if the backfill read them
    └── <trip uuid>.ics     the events of one trip
```

The trip JSON files are the archive, and the run makes both ICS files from them. Both commands write to this one archive:

1. The feed run adds and replaces the events of each trip in the feed, and it applies the delete rule in [Calendar feed](#calendar-feed).
2. The backfill adds the v2 objects of each trip. For a trip that has no events, it downloads the events from "Export trip to calendar". The v2 objects stay as the backfill read them.

The phases:

1. The scheduled feed run, which fills the archive. No login.
2. The one-time backfill, which adds the older trips and the v2 objects to the same archive.

## Open questions

Answer these with the real account:

1. Which of the browser headers does the download URL need? Does TripIt accept the cookie from an address that is not the browser address?
2. Does TripIt read the file name at the end of the download URL, or can the backfill send any name?
3. Does TripIt reject an `it_session_id` value after its 15 days, when no new session used it? Which status does TripIt return for a rejected value? [Issue 19](https://github.com/aaronspruit/tripit-exporter/issues/19) holds the test.

The client sends the headers of a Firefox 155 request on the TripIt website
with every request: the website's own API call for a web API v2 route
(`X-TRIPIT-APP-INFO`, `Sec-Fetch-Mode: cors`), and a browser navigation for
the download URL (`Upgrade-Insecure-Requests` and the four `Sec-Fetch-*`
headers). It sends `<trip uuid>.ics` as the file name. The website request
also holds `X-CSRF-Token-WA` and two Dynatrace headers, `x-dtpc` and
`x-dtreferer`; the client leaves them out, because a GET works without them.

On 2026-09-16 the operator ran the backfill in Docker on the operator
machine, which has the same public address as the browser. The download
returned the events of two trips with that header set and that file name.
The run did not test a smaller header set, a different address, or a
different file name, so questions 1 and 2 stay open for those cases.

The same run listed 191 trips, then TripIt sent a TCP reset to the detail
request of the third trip, about 10 seconds and 12 requests after the
first request. A second run started a few minutes later. The list request
for page 4 got no response for 60 seconds, after only 4 requests. TripIt
therefore throttles by the requests of the last few minutes, and a stalled
request is a second form of the same throttle. The client waits 5 seconds
before each request, and it waits out a throttled request inside the run.
The client applies the Akamai `Set-Cookie` headers to its next request, as a
browser does. In a third run with verbose mode, TripIt held a request with no response
twice: after 51 and after 57 requests in the previous 10 minutes. The last
response before each stall was a normal `200`, with no `Retry-After` and no
rate-limit header. Each stall ended after about 3 minutes and 50 seconds. The
limit is therefore about 50 requests in 10 minutes, and TripIt shows it only
as a request that gets no response.

## Sources

- [TripIt API v1 documentation](https://tripit.github.io/api/doc/v1/)
- [TripIt Public API](https://help.tripit.com/en/support/solutions/articles/103000391296-tripit-public-api)
- [Past trips on your calendar](https://help.tripit.com/en/support/solutions/articles/103000063392-past-trips-on-your-calendar)
- [Export an individual trip to your calendar](https://help.tripit.com/en/support/solutions/articles/103000063293-export-an-individual-trip-to-your-calendar)
- [Calendar feed setup and sync](https://help.tripit.com/en/support/solutions/articles/103000063280-calendar-feed-setup-and-sync)
- [Plans not appearing in calendar](https://help.tripit.com/en/support/solutions/articles/103000063363/)
- [Sign in or out of your account](https://help.tripit.com/en/support/solutions/articles/103000063295-sign-in-or-out-of-your-account)
- [caseyg/caseys-claude tripit-export skill](https://github.com/caseyg/caseys-claude/blob/main/skills/tripit-export/SKILL.md)
- [DevSecNinja/ai-toolkit tripit-exporter skill](https://github.com/DevSecNinja/ai-toolkit/blob/main/.apm/skills/tripit-exporter/SKILL.md)
- [schloo/TripRip](https://github.com/schloo/TripRip)
- [jschnurr/tripit-export](https://github.com/jschnurr/tripit-export)
