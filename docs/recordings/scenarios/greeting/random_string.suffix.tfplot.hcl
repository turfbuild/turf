turf {
  intent = "A short random suffix to disambiguate the name."

  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

resource "random_string" "suffix" {
  length  = 4
  special = false
  upper   = false
}
