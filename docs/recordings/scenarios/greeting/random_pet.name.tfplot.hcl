turf {
  intent = "The base pet name."

  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

resource "random_pet" "name" {
  length = 2
}
