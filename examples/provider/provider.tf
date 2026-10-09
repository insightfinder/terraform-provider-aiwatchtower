# Copyright (c) InsightFinder Inc.
# SPDX-License-Identifier: MPL-2.0

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

variable "username" {
  description = "AI WatchTower user name, sent as the X-User-Name header"
  type        = string
  sensitive   = true
}

variable "api_key" {
  description = "AI WatchTower API key, sent as the X-Api-Key header"
  type        = string
  sensitive   = true
}

# Notes:
# - Every attribute above can instead be provided via an environment variable
#   (AW_BASE_URL, AW_USERNAME, AW_API_KEY), letting the provider block be left
#   empty: `provider "aiwatchtower" {}`.
