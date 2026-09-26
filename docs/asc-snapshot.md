# App Store snapshot: `asc-snapshot.json` (schema version 1)

The Stuck tab's App Store conditions (lacquer #424b) are computed from a file.
**lacquer never holds App Store Connect credentials and never calls the ASC API.**
A separate producer writes the file on a schedule (fleet-ops' `asc-status --json`,
launchd, every 30–60 minutes), and lacquer only reads it.

## Where

`<inbox dir>/asc-snapshot.json`, next to the inbox file. With the default inbox
this is `~/.local/state/lacquer/asc-snapshot.json`.

The producer writes to a temporary name in the same directory and renames it
over the file, so a reader never sees half of one. File mode 0600.

## Shape

```json
{
  "schemaVersion": 1,
  "generatedAt": "2026-09-25T14:05:00Z",
  "producer": "fleet-ops asc-status 1.4.0",
  "errors": [
    { "bundleId": "com.example.flare", "message": "HTTP 401 from /v1/apps/…/appStoreVersions" }
  ],
  "apps": [
    {
      "bundleId": "com.patrickserrano.dailybread",
      "name": "Daily Bread",
      "ascAppId": "1234567890",
      "versions": [
        {
          "id": "abcd-…",
          "platform": "IOS",
          "versionString": "1.9",
          "appStoreState": "WAITING_FOR_REVIEW",
          "releaseType": "AFTER_APPROVAL",
          "stateSince": "2026-09-24T09:12:00Z",
          "stateSinceSource": "reviewSubmission.submittedDate",
          "buildId": "efgh-…",
          "buildNumber": "412"
        }
      ],
      "builds": [
        {
          "id": "efgh-…",
          "platform": "IOS",
          "number": "412",
          "versionString": "1.9",
          "processingState": "VALID",
          "expired": false,
          "uploadedDate": "2026-09-24T08:40:00Z",
          "attachedVersionId": "abcd-…"
        }
      ]
    }
  ]
}
```

Every timestamp is RFC 3339. Unknown fields are ignored, so the producer may add
fields without a schema bump. Removing or retyping a field is a new
`schemaVersion`.

### Top level

| Field | Type | Meaning |
|---|---|---|
| `schemaVersion` | int, required | `1`. Any other value is "couldn't check: unsupported schema version N". |
| `generatedAt` | time, required | When the producer finished reading ASC. Staleness is measured from this, never from the file's mtime. |
| `producer` | string | Free text, shown in problems ("from fleet-ops asc-status 1.4.0"). |
| `errors` | array | What the producer could not read. Each entry: `message` (required) and `bundleId` (optional; absent means the whole run). Each one is shown as a "couldn't check" row. A run that failed completely still writes a snapshot, with `apps: []` and an entry here, so the tab says why rather than going stale. |
| `apps` | array, required | One per app the producer covers. `[]` with no `errors` means "checked, the producer covers no apps"; lacquer shows that as a problem ("snapshot lists no apps"), never as "nothing stuck". |

### `apps[]`

| Field | Type | Meaning |
|---|---|---|
| `bundleId` | string, required | Identifies the app in row keys. |
| `name` | string, required | Shown in the row. |
| `ascAppId` | string | Used for the row's link: `https://appstoreconnect.apple.com/apps/<ascAppId>/distribution`. |
| `versions` | array, required | Every App Store version that is not `READY_FOR_DISTRIBUTION` (ASC's current name for live; the older `READY_FOR_SALE` means the same and is treated alike), `REPLACED_WITH_NEW_VERSION` or `REMOVED_FROM_SALE`, plus the current live one per platform (lacquer uses it only to decide which builds are superseded). |
| `builds` | array, required | The newest builds per platform, at least the 5 newest by `uploadedDate` that are not expired. |

### `versions[]`

| Field | Type | Meaning |
|---|---|---|
| `id` | string, required | ASC appStoreVersion id. |
| `platform` | string, required | `IOS`, `MAC_OS`, … as ASC spells it. |
| `versionString` | string, required | e.g. `2.1.1`. |
| `appStoreState` | string, required | ASC's value, copied verbatim, never mapped. |
| `releaseType` | string | ASC's value (`AFTER_APPROVAL`, `MANUAL`, `SCHEDULED`). |
| `stateSince` | time, required when `appStoreState` is one lacquer checks | When the version entered its current state. See below. |
| `stateSinceSource` | string, required with `stateSince` | Where `stateSince` came from, below. |
| `buildId`, `buildNumber` | string or null | The attached build. |

### `builds[]`

| Field | Type | Meaning |
|---|---|---|
| `id` | string, required | ASC build id. |
| `platform` | string, required | |
| `number` | string, required | CFBundleVersion. |
| `versionString` | string | CFBundleShortVersionString. |
| `processingState` | string, required | ASC's value verbatim (`PROCESSING`, `VALID`, `INVALID`, `FAILED`). |
| `expired` | bool, required | |
| `uploadedDate` | time, required | |
| `attachedVersionId` | string or null, required | The `versions[].id` this build is attached to, or null. |

## `stateSince`: how long a version has been in its state

The thresholds are "in this state for N hours", and the ASC API has no
"state changed at" field. So the producer records it, and says how:

| `stateSinceSource` | Use for | How the producer gets it |
|---|---|---|
| `reviewSubmission.submittedDate` | `WAITING_FOR_REVIEW` | The `submittedDate` of the version's current review submission. Exact. |
| `firstSeen` | `REJECTED`, `DEVELOPER_REJECTED`, and any state with no API timestamp | The producer keeps its own record of (version id, state) → the `generatedAt` of the first run that saw that version in that state, and starts again when the state changes. On the producer's first run, or after its record is lost, this is that run's time: it can only **under**-count, never flag something early. |

lacquer never uses a version's `createdDate` as `stateSince`: a version created
weeks ago and rejected an hour ago is not stuck.

When the source is `firstSeen`, the row says "at least" ("rejected for at least
4h12m"), because the real time may be longer.

## What lacquer reads from it (the three conditions)

Thresholds are the operator's (lacquer #424) and are not raised here.

| Condition | Rule | Threshold | Row key |
|---|---|---|---|
| Rejected | `appStoreState` is `REJECTED` or `DEVELOPER_REJECTED`, measured from `stateSince` | 3h | `asc-rejected:<bundleId>:<platform>:<versionString>` |
| Build not attached | a build with `processingState` `VALID`, `expired` false, `attachedVersionId` null, that is the newest (`uploadedDate`) build of its app and platform, and newer than the build of every version of that platform in `versions`; measured from `uploadedDate` | 3h | `asc-unattached:<bundleId>:<platform>:<number>` |
| Waiting for review | `appStoreState` is `WAITING_FOR_REVIEW`, measured from `stateSince` | 24h | `asc-waiting:<bundleId>:<platform>:<versionString>` |

"Newest and newer than every attached build" is what keeps a superseded build
that was never submitted from sitting on the tab forever.

A rejected row's detail says: *If you've replied in Resolution Center, dismiss
this until Apple responds.* The ASC API does not show a re-review after a
Resolution Center reply (A Bible Verse: Daily 2.1.1, 2026-09-25), so the
version stays `REJECTED` while Apple is in fact reviewing it. Dismissal
(`x`, a period, `stuck-dismissed.json`) is the answer, as for any other row.

## When the tab says "couldn't check"

Never an empty list in any of these cases:

- the file does not exist ("no snapshot at <path>; the producer is fleet-ops asc-status");
- it cannot be read, is not JSON, or lacks a required field;
- `schemaVersion` is not 1;
- `generatedAt` is more than **90 minutes** old ("snapshot is 2h10m old");
- `generatedAt` is more than 5 minutes in the future;
- `apps` is empty;
- once per entry in `errors`; the other apps' rows still show;
- a version in a checked state has no `stateSince`: that version is named, and
  the other rows still show.
