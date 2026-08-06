plot {
  version = 1
  name    = "greeting"
  intent  = "A tiny ad-hoc plot: a generated pet name with a random suffix, authored by the declare family and graduated to a tofu configuration with config_promote."

  backend "local" {
    path = "terraform.tfstate"
  }
}
