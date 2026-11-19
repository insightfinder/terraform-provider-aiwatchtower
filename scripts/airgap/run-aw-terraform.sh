#!/usr/bin/env bash
# First run of the bundled configuration on the airgapped machine, from inside the unpacked
# aw-airgap-bundle folder. Afterwards use terraform plan / apply / destroy from the same folder.
set -euo pipefail

export AW_BASE_URL=${AW_BASE_URL:-"https://<internal AI WatchTower URL>"}
export AW_USERNAME=${AW_USERNAME:-"<your user name>"}
export AW_API_KEY=${AW_API_KEY:-"<your api key>"}
export CHECKPOINT_DISABLE=1                # don't try to reach the internet for version checks
# export SSL_CERT_FILE=/path/to/ca.pem    # only if AI WatchTower uses a private certificate authority

MODEL=${MODEL:-aiwatchtower_model.this}    # the model resource in main.tf

cd "$(dirname "$0")"

# Installs the provider from ./terraform.d (no internet needed)
terraform init

# Everything that will be created: the model and the monitor with the expanded data quality rules
terraform plan

# Create the model first; its data source is set up in the background
terraform apply -target="$MODEL"
read -rp "Wait until the model's data source shows in the AI WatchTower UI, then press Enter... "

# Create the monitor
terraform apply

# Should say "No changes.": AI WatchTower now matches the configuration
terraform plan
