# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-09-28

### Added
- Initial provider: `base_url` / `username` / `api_key`, also readable from `AW_BASE_URL`, `AW_USERNAME` and `AW_API_KEY`. Authenticates with the `X-User-Name` + `X-Api-Key` header pair that AI WatchTower's `ApiKeyFilter` expects on `/api/external/v1/*`.
- **aiwatchtower_model**: manage models, identified by the `(user_name, model_name, model_version)` triple. Supports `model_type`, `environment`, `description`, `data_sources` and `ubl_dimension`, exposes `model_id`, `anomaly_count` and `monitor_info` as read-only, supports full create/read/update/delete, and can be imported with `user_name@model_name@model_version`.
  - Changing `model_version` is an in-place rename rather than a replacement: the provider fills the backend's `oldModelVersion` field from prior state, which moves the model record, its mapping relation and its monitors. `oldModelVersion` is not a schema attribute — the create endpoint rejects any payload carrying it.
  - `data_sources[].name` is documented as a project-name *suffix*. The backend derives the UIE project name as `<model_name>-<model_version>-<name>` with no validation, so a full project name here yields a doubled prefix pointing at nothing. The backend's `existInUIE` flag, the only way to reach that path, is deliberately not exposed.
  - The backend's `isNew` flag is set by the provider rather than exposed as an attribute: it is a request flag that `hasNewDataSource()` reads to decide whether to enqueue project creation, and the backend clears it once the project exists, so a user-settable value could only ever produce drift.

### Reliability
- Every request carries a 60-second per-attempt timeout and is cancelled when Terraform is interrupted; requests now run on the caller's context instead of a background one.
- Transient failures are retried up to three times with jittered exponential backoff, but only where it is safe: connection errors, `408`, `429` and `5xx` for `GET`/`PUT`/`DELETE`. `POST` is never retried on a status code, because a create whose response was lost has still happened and resending it returns a misleading 409. Other `4xx` responses are never retried at all.
- After a write the model is read back with a short retry, and if the read still fails the resource is written to state anyway: the model exists by then, and omitting it would strand it outside Terraform where the next apply is rejected as a conflict.
- Response bodies are read through a 16 MiB limit, and transport errors no longer quote the request URL, which can carry the user name.

### Fixed
- Writes no longer copy the backend's view of Optional attributes back over the plan. Terraform requires every non-Computed attribute to leave Apply exactly as it entered the plan, so a value the backend normalises — a defaulted `data_source`, a trimmed `description` — would have failed the apply with "Provider produced inconsistent result after apply". Only Computed attributes are refreshed after a write; reconciling the rest is Read's job.
- Adding a data source to an existing model now provisions its UIE project. `ModelService.update` gates the whole setup payload on `hasNewDataSource()` and `ModelSetupService` then skips any entry whose `isNew` is false, so the previous behaviour recorded the data source in the model metadata and silently created nothing. Entries already present in state are left unflagged so their projects are not re-provisioned.
- `Read` now maps an empty backend value to null rather than keeping the previous state value, so an attribute cleared outside Terraform shows up as drift instead of being masked.
- `DeleteModel` checks for the model before asking the backend to delete it, which turns the common "already deleted by hand" case into a no-op instead of a 500 that the retry layer would spend several seconds re-attempting.

### Known limitations
- Data source projects are provisioned asynchronously through a message queue, so a successful apply does not guarantee the UIE projects exist yet.
- Deleting a model that is already absent makes the backend fail the whole call (`ModelService.deleteModels` succeeds only when every key was deleted). The client re-reads the model on failure and treats "gone" as success, so `terraform destroy` stays idempotent.
