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
