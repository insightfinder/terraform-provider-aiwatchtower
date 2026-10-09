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
  - The backend's write-only `isNew` flag is set by the provider rather than exposed: it is what `hasNewDataSource()` checks to decide whether to enqueue project creation, and the backend clears it afterwards, so it can never be round-tripped through state.

### Known limitations
- Data source projects are provisioned asynchronously through a message queue, so a successful apply does not guarantee the UIE projects exist yet.
- Deleting a model that is already absent makes the backend fail the whole call (`ModelService.deleteModels` succeeds only when every key was deleted). The client re-reads the model on failure and treats "gone" as success, so `terraform destroy` stays idempotent.
