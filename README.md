# Terraform Provider for InsightFinder AI WatchTower

Manage AI WatchTower resources declaratively with Terraform.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- [Go](https://go.dev/doc/install) >= 1.25.8 (to build the provider; the floor comes from the
  `terraform-plugin-docs` version pinned in `tools.go` for documentation generation)
- An AI WatchTower user name and API key

## Provider Configuration

```hcl
terraform {
  required_providers {
    aiwatchtower = {
      source  = "insightfinder/aiwatchtower"
      version = "~> 0.1"
    }
  }
}

provider "aiwatchtower" {
  base_url = "https://ai.insightfinder.com"
  username = var.username
  api_key  = var.api_key
}
```

### Authentication

All three attributes can come from environment variables instead, which lets the provider block
be left empty:

```bash
export AW_BASE_URL="https://ai.insightfinder.com"
export AW_USERNAME="your-user-name"
export AW_API_KEY="your-api-key"
```

The provider authenticates with the `X-User-Name` and `X-Api-Key` headers against AI WatchTower's
`/api/external/v1` endpoints.

`api_key` is an AI WatchTower API key, which is not the same thing as a UIE license key — but in
practice the two often coincide: `ApiKeyService.createUserApiKey` registers the account's license
key alongside every generated key, so for an account that has ever had an API key created, the
license key shown on the UIE Account Info page is accepted here as well. If it is rejected, create
a dedicated key by calling `POST /api/v1/user/api-key?userName=<user>&apiKeyName=<name>` from a
logged-in AI WatchTower browser session.

## Example: Model

```hcl
resource "aiwatchtower_model" "fraud_guard" {
  model_name    = "Fraud-Guard"
  model_version = "V1.0.0"
  model_type    = "OpenAI"

  environment = "Production"
  description = "Fraud detection model"

  data_sources = [
    {
      name        = "InputLog"
      data_type   = "Log"
      data_source = "Custom"
    },
  ]
}
```

See [the resource docs](docs/resources/model.md) for the full field list.

## Resources

- [`aiwatchtower_model`](docs/resources/model.md) - Manage AI WatchTower models

## Reliability

Every API call is bounded by a 60-second per-attempt timeout and is cancelled when Terraform is
interrupted.

A failed call is retried up to three times with exponential backoff (1s, 2s, 4s, jittered), but
only when retrying is both safe and useful:

- **Retried:** connection failures, `408`, `429` and `5xx`, for `GET`, `PUT` and `DELETE`. Those
  methods converge on the same end state however often they are repeated.
- **Not retried:** `POST`. A create whose response was lost has still happened, and sending it
  again comes back as a spurious "model already exists" — true of a dropped connection as much
  as of a `500`, since neither says whether the server acted.
- **Never retried:** other `4xx` responses. A rejected key, a missing parameter or a conflicting
  model will be rejected identically every time; retrying only delays the real error.

After a write, the model is read back to populate the computed attributes, with a short retry in
case the read lands on a replica that has not caught up. If that read still fails, the resource is
written to state anyway — the model exists at that point, and omitting it would leave it stranded
outside Terraform, where the next apply would be rejected as a conflict.

## Known Limitations

**Data source projects are created asynchronously.** Creating a model with `data_sources` enqueues
project provisioning on a message queue; the UIE projects are created by a background consumer.
A successful apply means the model record exists, not that its projects are ready.

## Design notes

The source carries no commentary. [DESIGN.md](DESIGN.md) records the backend contracts and the
framework rules that shaped this provider — why `data_sources[].name` is a suffix, why `POST` is
never retried, why a write does not copy the backend's answer back over the plan, and the rest.
Read it before changing the client or the resource.

## Development

### Building

```bash
go build -o terraform-provider-aiwatchtower
```

### Installing locally

```bash
make install
```

This publishes the binary as version `1.0.0` under
`~/.terraform.d/plugins/registry.terraform.io/insightfinder/aiwatchtower/1.0.0/`, which a local
configuration can consume with `version = "~> 1.0"`.

### Testing

```bash
# Unit tests
make test

# Acceptance tests (creates real resources; requires AW_* credentials)
make test-acc
```

### Generating documentation

```bash
make docs
```

## License

Apache-2.0
