turf {
  intent = "The assembled name: pet plus suffix."
}

output "full_name" {
  value = "${random_pet.name.id}-${random_string.suffix.result}"
}
