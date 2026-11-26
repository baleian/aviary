# The values of the chart for Substrate in the local cluster: the connection
# strings of its database, from the Secret substrate-postgres, which `make
# substrate` writes as a values file.
(.data | map_values(@base64d)) as $passwords
| "@substrate-postgres.aviary-data:5432/substrate?sslmode=disable" as $database
| {substrate: {
    database: {
      owner: ("postgresql://substrate_owner_user:" + $passwords.owner + $database),
      readWrite: ("postgresql://substrate_readwrite_user:" + $passwords.readWrite + $database)
    }
  }}
