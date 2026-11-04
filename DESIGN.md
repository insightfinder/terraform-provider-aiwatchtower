# Design notes

Why this provider is built the way it is. The code itself carries no commentary, so anything here
that reads as arbitrary is usually a backend contract that cost something to discover.

Everything below was verified against the AI WatchTower backend on the branch deployed to the test
environment, or observed live against an AI WatchTower test deployment. Java class and method names refer
to the `AI-WatchTower-Backend` repository.

---

## 1. The API surface this provider can reach

AI WatchTower registers `ApiKeyFilter` on `/api/external/v1/*`, and it authenticates on the
`X-Api-Key` + `X-User-Name` header pair. Everything under `/api/v1` is guarded by `TokenFilter`
instead, which requires a browser session token.

**An API-key client cannot reach `/api/v1` at all, whatever headers it sends.** That is why the
provider is limited to the external endpoints, and why adding a resource for something that only
exists under `/api/v1` requires a backend change first, not a client-side workaround.

`X-Api-Key` is the exact header name `ApiKeyFilter` reads. Renaming it produces a 401 that explains
nothing, so `TestDoRequestSendsAuthHeaders` pins it.

An AI WatchTower API key is not the same thing as a UIE license key, but in practice the two often
coincide: `ApiKeyService.createUserApiKey` registers the account's license key alongside every
generated key, so for an account that has ever had an API key created, the UIE license key is
accepted here too.

## 2. The model resource

### 2.1 Identity and renaming

A model is identified by the triple `(userName, modelName, modelVersion)`.

`PUT /api/external/v1/model` accepts `oldModelVersion` alongside `modelVersion`. When the two differ
and `oldModelVersion` is non-empty, `ModelDTO.isUpdateModelVersion()` sends the backend down a
**rename** branch: the model record, its mapping relation and its monitors all move, and the backend
issues a fresh `modelId`. That is why `model_version` is not `RequiresReplace` — changing it is an
in-place update.

Two consequences:

- An ordinary edit must send the *unchanged* current version as `oldModelVersion` to stay on the
  plain update path.
- `oldModelVersion` must come from prior state, never from user configuration. It is not a schema
  attribute, and **the create endpoint rejects any payload that carries it**.

`modelID()` and `parseModelImportID()` must remain exact inverses: the import ID and the resource's
own `id` attribute have to be the same string, or the same model gets different ids depending on
whether it was imported or created.

The import ID is `user_name@model_name@model_version`. An AI WatchTower user name is routinely an
email address, so the ID normally contains three `@` characters and only the last two are
structural. **It must be split from the right.** A naive split on `@` rejects every realistic ID.

### 2.2 Data sources and the doubled-prefix trap

`data_sources[].name` is a **suffix**, not a project name. The backend derives the UIE project name
as `sanitize(modelName + "-" + modelVersion + "-" + name)` and does not validate the result against
UIE.

Passing an already-qualified project name therefore yields something like
`Fraud-Guard-V1-0-0-Fraud-Guard-V1-0-0-OutputLog`, which resolves to nothing: downstream calls
return 204 and monitors become uncreatable.

The backend's `existInUIE` flag on `ModelDataSource` is the only way to reach that code path, and
this provider deliberately does not expose it.

### 2.3 `isNew` gates provisioning, and the create and update paths differ

`isNew` on a data source is a **request flag, not state**:

- `ModelDTO.hasNewDataSource()` reads it to decide whether to enqueue project creation at all.
- `ModelSetupService` clears it once the project exists, so a read always returns `false` regardless
  of what was sent. (It reads back at all only because the GET endpoint serialises with Gson, which
  ignores the `@JsonProperty(WRITE_ONLY)` that governs the Jackson-parsed request side.)

Because the backend owns the value, exposing it as a schema attribute could only ever produce drift.
The provider sets it itself — and the two paths need different values:

| Path | Backend behaviour | What the provider sends |
|---|---|---|
| Create | `ModelService.create` calls `sendProjectCreationPayload(..., isUpdate=false)` unconditionally, and `ModelSetupService` provisions every entry when `isUpdate` is false | `isNew=true` on all entries |
| Update | `ModelService.update` gates the whole payload on `hasNewDataSource()`, and `ModelSetupService` then skips any entry whose `isNew` is false | `isNew=true` only on entries absent from prior state |

**This is why adding a data source to an existing model used to do nothing.** The entry was recorded
in the model metadata, no project was created, and the apply reported success. Entries already in
state must keep `isNew=false` so their projects are not re-provisioned.

`data_sources` and `monitor_info` are **sets, not lists**. `ModelDetail` keeps both in a Java
`Set`, so the backend returns them in hash order rather than the order they were sent. Modelling
them as ordered lists made Terraform report the reshuffle as a diff, which apply could never
settle: every apply rewrote the order and the backend shuffled it back. Unit tests cannot catch
this — a stub server replays whatever order it was given — so it only showed up against the real
API.

### 2.4 Provisioning is asynchronous

`POST`/`PUT` only enqueue a `ModelSetupPayload` on RabbitMQ. `ModelSetupConsumer` afterwards creates
the UIE projects, adds them to a system and writes the mapping rows.

**HTTP 200 means the model record exists, not that its data sources are ready.** The resource emits
a warning whenever it enqueues this work.

### 2.5 Reading a model

`GET /api/external/v1/model` returns a **bare JSON array** of `ModelDTO`, not the `IFResponse`
envelope the write endpoints use.

`modelName` and `modelVersion` are **optional filters**, so the response may contain entries other
than the requested one. The client narrows the array itself rather than assuming a single match.

`environment` and `description` can be absent at the top level of an entry and present instead
inside the nested `modelMetadata` object, so both places have to be read.

`GetModel` returns `(nil, nil)` — not an error — when the model is absent, because Read turns that
into "resource gone from state" rather than a failed plan.

The API cannot distinguish "unset" from "set to the empty string": both come back empty. The
provider maps empty to null, so the two are indistinguishable in state.

### 2.6 Deleting a model

`DELETE /api/external/v1/model` takes a **list** of keys, and `ModelService.deleteModels` reports
success only when *every* key was deleted. Asking it to delete a model that is already gone fails
the whole call with HTTP 500.

Terraform has to tolerate that — destroying a resource somebody removed by hand must not error — so
`DeleteModel` checks existence first, which turns the routine case into a no-op instead of a 500 the
retry layer would spend several seconds re-attempting. It re-checks after a failed delete in case
the model disappeared between the two calls.

In the payload, the backend's `ModelKey` names its version field **`version`, not `modelVersion`**.
Sending the wrong name deserialises to null: the request still looks well-formed and succeeds while
matching and deleting nothing.

This endpoint did not exist originally; it was added to `ModelExternalController` for this provider,
delegating to the same `ModelApiHandler` the internal controller uses.

---

## 3. Terraform framework contracts

### 3.1 A write must not echo the backend back over the plan

Terraform requires every **non-Computed** attribute to leave Apply exactly as it entered the plan.
Copying a backend-normalised value back — a defaulted `data_source`, a trimmed `description` — fails
the entire apply with *"Provider produced inconsistent result after apply"*.

So the post-write refresh (`refreshComputed`) touches **Computed attributes only**. Reconciling
user-set attributes with backend reality is Read's job, where a difference is drift to report rather
than a contract violation.

`applyConfigToModel`, used by Read, is deliberately the opposite: it copies the backend's full view,
including Optional attributes, and maps an attribute the backend no longer has to **null**, so a
value cleared outside Terraform shows up in the next plan instead of being masked by stale state.

### 3.2 A failed read-back must still write state

If the write succeeded but the read-back did not, the model exists in AI WatchTower. Returning an
error without writing state strands it: the next apply attempts a fresh create and is rejected with
a conflict.

The provider therefore persists what it knows, zeroes the Computed attributes (they cannot remain
unknown once Apply finishes) and still surfaces the read failure. The next refresh corrects them.

For Update this matters even more, because the write may have included a version rename — leaving
the old state in place would leave Terraform identifying the model by a version the backend no
longer has.

### 3.3 Read-back retries

The write is synchronous as far as the model record goes, but an immediate read can hit a Cassandra
replica that has not caught up and return a well-formed 200 carrying an **empty list**. That is not
an error response, so the HTTP-level retry does not cover it. `refreshComputed` retries a few times
over a short window — kept small so a genuinely missing model does not become a long stall.

### 3.4 Credentials in the schema

`username` and `api_key` are marked `Sensitive`, which governs plan and state rendering. It has **no
effect on `tflog`**: a field written with `tflog.SetField` is logged in clear unless it is also named
in `MaskFieldValuesWithFieldKeys`. Both are masked explicitly for that reason.

---

## 4. HTTP client

### 4.1 Timeouts

No `Timeout` is set on the `http.Client` itself. Each attempt gets its own deadline from the request
context, which also makes an in-flight attempt cancellable when Terraform is interrupted.

The default is 60 s per attempt. Model writes fan out to RabbitMQ and Cassandra on the backend, so
they are slower than a plain CRUD service, but an attempt that has not answered within that window
is treated as lost rather than waited on.

### 4.2 Retries

`MaxRetries` counts **additional** attempts after the first, so the default of 3 means at most four
sends. Backoff starts at 1 s and doubles (1 s, 2 s, 4 s) with jitter, so several resources retrying
inside the same apply do not synchronise. `RetryBaseDelay` is overridable so tests can run fast.

What is retried, and why:

- **408, 429 and 5xx** — for this backend a 5xx is usually a Cassandra or RabbitMQ hiccup that
  clears in seconds; 408 and 429 are explicit "try again" signals.
- **Other 4xx: never.** A bad key, a missing parameter or a conflicting model is a deterministic
  rejection, and retrying only delays the real error.
- **Idempotent methods only** (`GET`, `PUT`, `DELETE`). The model API's update and delete converge
  on the same end state however often they are repeated.
- **`POST`: never**, on any failure. A second create is answered with 409, so a create that
  succeeded but whose response was lost would come back as a spurious "model already exists".
  Neither a dropped connection nor a 500 tells the client whether the server acted, so the genuine
  transient failure is surfaced instead of being papered over with a misleading conflict.

When the retry budget is exhausted, `DoRequest` returns the **last attempt's status and body** with a
nil error rather than collapsing it into a Go error, so callers still see the backend's own response.

### 4.3 URL construction

Request URLs are built by plain concatenation of base URL and path — there is no URL-joining helper —
so trailing slashes and surrounding whitespace are stripped from the configured base URL at
construction. Otherwise a base URL ending in `/` yields `//api/external/v1/...`, which most but not
all servers tolerate.

### 4.4 Errors must not quote the URL

`net/http` returns a `*url.Error` whose message quotes the **whole request URL**, and this API
carries `userName` as a query parameter. Wrapping that error bare — and formatting the path, which
includes the query string — put the user name in the message **twice**.

Errors reach the operator and Terraform logs, so `doOnce` reduces them: the bare path without the
query string, plus the transport cause unwrapped from the `*url.Error`. The host and the underlying
failure survive, which is what makes the message useful.

`TestDoRequestDoesNotLeakCredentialsInErrors` goes through `GetModel` rather than `DoRequest`,
because `GetModel` is what builds the query string — the leak only exists on a path that has one. An
earlier version of that test asserted only that the API key was absent, which held vacuously: the
key travels exclusively in a header and could never appear in a URL.

### 4.5 Response size

Reads are capped at 16 MiB. The largest expected body is the model list, a few hundred kilobytes;
beyond that is a malfunction or a captive-portal HTML page, and reading it in full helps nobody.

---

## 5. Build and docs

`tools.go` carries a `tools` build constraint and a blank import of `tfplugindocs`. The constraint is
load-bearing: it keeps the code-generation binary out of every real build while still recording it in
`go.mod`, so `go run .../tfplugindocs` resolves to a pinned version instead of failing with
*"no required module provides package"*. This is what raises the module's minimum Go version.

Registry documentation under `docs/` is **generated, not hand-written**. `go generate` runs
`terraform fmt -recursive ./examples/` first — `tfplugindocs` embeds those example files verbatim —
and then `tfplugindocs` itself. Anyone changing a schema or an example must re-run it.

`tfplugindocs` infers the provider name from the **directory name**, so the checkout has to be called
`terraform-provider-aiwatchtower`. In a differently-named directory it looks for resources under the
wrong prefix and fails.

Example files follow the tool's conventions: `examples/resources/<resource>/resource.tf` and
`import.sh` are embedded into the generated page automatically. `templates/resources/model.md.tmpl`
supplies the prose sections around the generated schema.

The `version` variable in `main.go` is stamped at build time by goreleaser; the `"dev"` literal is
only the local fallback. It is surfaced through `MetadataResponse.Version`, and the framework
convention is `"dev"` locally and `"test"` under acceptance tests.

---

## 6. Releasing

A `v*` tag runs `.github/workflows/release.yml`, which imports the signing key and hands the build
to GoReleaser. The Terraform Registry then ingests that GitHub Release, so the asset names are a
contract, not a preference:

- `<project>_<version>_<os>_<arch>.zip`, each holding a binary named `<project>_v<version>`
- `<project>_<version>_SHA256SUMS` and its detached signature `..._SHA256SUMS.sig`
- `<project>_<version>_manifest.json`

The manifest needs **two** separate `extra_files` blocks in `.goreleaser.yml` — one under
`checksum:` and one under `release:` — with byte-identical `name_template`s. They are independent
keys: the checksum pipe runs before the publish pipe, so an entry under `release:` alone uploads a
manifest that `SHA256SUMS` does not list, and an entry under `checksum:` alone lists a file that was
never uploaded. HashiCorp's own scaffolding writes both.

Without the manifest the Registry falls back to declaring **protocol 5.0**. This provider serves
**6.0** — `main.go` leaves `ServeOpts.ProtocolVersion` unset and terraform-plugin-framework defaults
to `tf6server` — so the omission does not fail the release, it silently advertises the provider to
Terraform versions that cannot run it.

`terraform-registry-manifest.json` must be committed. The release runner checks out only tracked
files, and GoReleaser aborts on a glob that matches nothing.

The actions in `release.yml` are pinned to commit SHAs rather than tags, because that workflow is
handed the GPG private key and a mutable tag can be repointed. The pinned revisions are:

| Action | Pinned SHA | Equivalent tag |
|---|---|---|
| `actions/checkout` | `11d5960a326750d5838078e36cf38b85af677262` | v4 |
| `actions/setup-go` | `40f1582b2485089dde7abd97c1529aa768e1baff` | v5 |
| `crazy-max/ghaction-import-gpg` | `e89d40939c28e39f97cf32126055eeae86ba74ec` | v6 |
| `goreleaser/goreleaser-action` | `e435ccd777264be153ace6237001ef4d979d3a7a` | v6 |

`VERSION` is read by nothing: the published version comes from the git tag, and `main.version` is
stamped by GoReleaser's ldflags. It exists to record the intended next version, and the `Makefile`
install path and the README's local-install instructions are kept in step with it by hand.

---

## 7. The monitor resource

### 7.1 What a monitor is, and what the API will let you change

A monitor is keyed by `MonitorKey(userName, monitorName)` and watches one model's data sources for
one `MonitorType`. `MonitorExternalController` is unusual among the external controllers in
offering a complete CRUD set, so no backend change was needed to manage one.

`MonitorUpdateDTO` carries exactly three mutable fields — `monitorDesc`, `monitorScope` and
`monitorSetting` — and `MonitorService.updateMonitor` applies each only when it arrives non-null.
Nothing else about a monitor can be changed after creation, which is why `monitor_name`,
`monitor_type`, `model_name`, `model_version`, `environment`, `data_sources` and `user_name` all
carry `RequiresReplace`.

That same "only non-null fields are applied" rule means an emptied description or scope has to be
sent explicitly. `monitorUpdateDTO` therefore drops `omitempty` from both, and `toUpdateDTO`
substitutes an empty slice for a nil scope. `TestUpdateMonitorSendsClearedFieldsExplicitly` pins
it; with `omitempty` restored the test fails and the backend silently keeps the old value.

`isValidMonitorCreationRequest` refuses a duplicate monitor name with 409 and a second monitor of a
type the model already carries with 424. Both are surfaced with the backend's own message rather
than a generic HTTP error.

### 7.2 Writing and reading a monitor are different shapes

The write shape is `MonitorDTO`, the read shape is `MonitorUIDTO`. They disagree on the version
field: the request sends `modelVersion`, the response returns `version`, alongside
`monitorNameDisplayName`, `modelNameDisplayName` and `versionDisplayName`.

Those display names are not safe to read. `MonitorApiHandler.applyVersionDisplayMap` rewrites them
through `ModelUtil.replaceUsingMap` against the trained-model display map, so they are a rendering
of the name rather than the name. The resource reads the raw `monitorName`, `modelName` and
`version` fields only.

A successful update answers **201**, not 200: `MonitorService.updateMonitor` returns its
`ApiResponse` with `HttpStatus.CREATED`, the same status the create path uses. A client that
accepts only 200 reports every successful update as a failure, and a stub test server cannot catch
it because the stub decides the status itself. Both writes accept 200 and 201; delete answers 200,
because it is built from `ResponseEntity.ok` in the controller rather than from an `ApiResponse`.

### 7.3 A missing monitor and a forbidden one are the same answer

`MonitorService.getSingleByUserInSameTenantPermissionFilter` returns null both when the monitor
does not exist and when the caller may not see it, and the handler turns either into
403 `No permission to access the monitor`. There is no 404.

Treating that 403 as an error breaks the ordinary drift case — someone deletes a monitor in the UI
and every subsequent plan fails. Treating it as "gone" silently drops a managed resource out of
state the first time a permission is misconfigured.

`GetMonitor` disambiguates by listing the user's monitors: the list endpoint is the same
controller and the same permission, so a list that returns 200 proves the caller may read monitors,
which makes the 403 on the single monitor mean it is gone. A list that is also refused reports the
error. `TestGetMonitorTreatsForbiddenAsAbsentWhenTheListIsReadable` and
`TestGetMonitorReportsForbiddenWhenTheListIsAlsoForbidden` pin both halves.

The same 403 reaches `DeleteMonitor`, which is why it reads the monitor first rather than
inspecting the delete's own status: `MonitorApiHandler.delete` calls `deleteByKeys` and reports
success without checking that anything was removed.

### 7.4 The backend drops data sources without saying so

`MonitorService.saveMonitor` filters the request's data sources through `isDataSourceCompatible`
and keeps only `qualifyingDataSources`. A `Metric` data source is rejected by every type except
`MODEL_PERFORMANCE`, and `LLM_TRUST_AND_SAFETY` keeps nothing but `Trace`, because the setting is
derived from the data source's own log or trace records. The monitor is then stored with whatever
survived, and the call returns success.

Terraform cannot express that. `data_sources` is not computed, so a write has to return what the
plan asked for; writing back the filtered list is an inconsistent result, and writing back the
planned list leaves state claiming a data source that was never stored.

So the resource compares the write against the read-back with `client.DroppedDataSources` and fails
the apply, naming what was dropped. State is written from the backend's own answer first, so the
resource is recorded, marked tainted and replaced on the next apply rather than being orphaned.

The rule is deliberately not reimplemented client-side as a plan-time validator: a copy of backend
logic goes stale silently, and comparing against the read-back cannot.

### 7.5 `environment` is resolved, not stored

`MonitorService.resolveEnvironment` falls back to the model's own `ModelDetail.environment` when
the monitor carries none. A monitor created without an environment therefore reads back with the
model's, which is why the attribute is `Optional` **and** `Computed`.

`monitorScope` has the mirror-image problem: `Monitor.getScope()` returns `new ArrayList<>()`, not
null, when nothing is stored. The read path maps an empty list back to null so an absent scope
stays absent.

### 7.6 `monitorSetting` is polymorphic, and only one shape is modelled

`MonitorSetting` has nine implementations, selected by a `type` discriminator. Modelling all nine
as typed attributes is a large schema for little gain, and modelling none of them gives up
validation and documentation entirely.

The resource does both: `data_quality` is the full typed form of `DataQualitySetting`, and
`monitor_setting_json` carries any of the other eight verbatim. `ValidateConfig` keeps them
mutually exclusive, requires `data_quality` for a `DATA_QUALITY` monitor and forbids it for every
other type.

A `DATA_QUALITY` monitor is held to the typed form because of 7.7: the defaults the backend writes
into the setting cannot be reconciled against a raw JSON document, so the JSON form would diff
forever. Some other types normalize their setting on write — `LLMTrustAndSafetySetting.validateAndNormalize`
does — and those are documented as needing `ignore_changes`.

The setting is serialized by the backend's **Gson**, not Jackson: `Monitor.setSetting` gzips
`GsonUtility.gson.toJson(...)` into a `ByteBuffer`. Gson names JSON after the Java field, so the
Go field tags follow the field names, not the accessors. `isPrimaryKey` is the one that bites:
the Java field is a boxed `Boolean` precisely so the accessor is `getIsPrimaryKey()`, and a client
sending `primaryKey` is silently ignored. `TestDataQualitySettingUsesTheBackendFieldNames` pins it.

`GsonUtility.gson` does not serialize nulls, so an unset field is absent from storage and absent
again on read. Every optional field in the Go shape is therefore a pointer with `omitempty`, so
"unset" survives the round trip instead of becoming a zero value.

### 7.7 The backend rewrites a data quality setting before storing it

Both `saveMonitor` and `updateMonitor` call `applyVolumeDefaults()` and
`applySchemaCheckDefaults()` on a `DataQualitySetting` before it is persisted, unconditionally —
not only when volume detection or schema checking is enabled. They set `volumeDetectionMode` to
`AI`, `volumeSensitivity` to `MEDIUM_HIGH`, the baseline factors to 1.08 and 1.15, and each unset
`schemaCheck` sub-check to false.

Those six attributes are `Optional` and `Computed`, and `fillComputedDataQuality` writes **only the
values the plan left unknown**. Anything the plan already settled is sent to the backend and must
come back unchanged; overwriting it from the read-back is the inconsistent-result bug of 3.1, one
level deeper. `TestFillComputedDataQualityOnlyWritesUnknowns` pins the distinction, and
`TestDataQualityToClientOmitsUnknownComputedAttributes` pins the other half: an unknown attribute
is left out of the request so the backend is the one that decides it.

`volumeThresholdsEnabled` is deliberately **not** exposed. The backend keeps it as a legacy mirror
of `volumeDetectionMode == MANUAL` and rewrites it on every save. A computed-only attribute keeps
its prior state in a plan, so it would read stale the moment the mode changed, and forcing it
unknown would need a plan modifier reaching across attributes. It carries no information that
`volume_detection_mode` does not.

`volumeThresholds` is *not* defaulted on write — `getVolumeThresholdsOrDefault()` applies 20/40 at
read time elsewhere — so it round-trips verbatim and stays a plain `Optional`.

### 7.8 Shapes inside the data quality setting

`dataQualityTypes` is a Java `Set`, so it is a Terraform set, for the same reason `data_sources`
is: a list would never converge against the order the backend returns. `consistencyKeys` is a
`List` and stays ordered.

`groupKeys` is a `Map<String, Map<String, FieldSetting>>`. Terraform has no map-of-map-of-object
nested attribute, and expressing it as a raw `MapAttribute` of object element types would make
every field mandatory and drop the per-attribute documentation. It is modelled as a map of objects
each holding a `fields` map instead, which costs one extra level in HCL and keeps the whole shape
typed.

`VolumeReferenceDTO` is reachable from Terraform even though the Workbench uploads it as a file:
it is two flat lists once parsed. `VolumeReferenceService.pushReference` calls its `normalize()`,
which upper-cases `dayOfWeek`, before the reference reaches UIE, so a lower-case weekday is
accepted. The schema still documents the exact spellings the backend validates against.

Volume detection itself was verified live: `volume_detection_enabled`, `MANUAL` mode and
`volume_thresholds` apply on a first create and re-plan clean. `volume_reference` is the one part
that could not be exercised. Every push of a reference, down to one holiday and one default volume
entry, on a model created minutes earlier, comes back as a generic "Monitor update failed". The
payload clears the backend's own `VolumeReferenceDTO.validate()` first, so the failure is inside
AW's call to UIE's `/api/v1/volumereference`, not in what this provider sends. The attribute is
kept, because the shape is pinned by both sides' DTOs, but it is untested against a live backend.

### 7.9 Import

A monitor imports as `user_name@monitor_name`, split on the **last** separator so an email address
user name survives. This is the same rule, and the same trap, as the model resource's three-part
identifier.
