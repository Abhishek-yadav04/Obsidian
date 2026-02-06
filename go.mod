module github.com/corazawaf/coraza/v3

go 1.23

// Testing dependencies:
// - go-mockdns
// - go-modsecurity (optional)

// Development dependencies:
// - mage

// Build dependencies:
// - libinjection-go
// - aho-corasick
// - gjson
// - binaryregexp
// - ocsf-schema-golang

require (
	github.com/anuraaga/go-modsecurity v0.0.0-20220824035035-b9a4099778df
	github.com/corazawaf/coraza-coreruleset v0.0.0-20240226094324-415b1017abdc
	github.com/corazawaf/libinjection-go v0.2.3
	github.com/foxcpp/go-mockdns v1.2.0
	github.com/gorilla/websocket v1.5.3
	github.com/jackc/pgx/v5 v5.8.0
	github.com/jcchavezs/mergefs v0.1.1
	github.com/joho/godotenv v1.5.1
	github.com/kaptinlin/jsonschema v0.6.9
	github.com/magefile/mage v1.15.1-0.20250615140142-78acbaf2e3ae
	github.com/mccutchen/go-httpbin/v2 v2.20.0
	github.com/petar-dambovaliev/aho-corasick v0.0.0-20250424160509-463d218d4745
	github.com/prometheus/client_golang v1.23.2
	github.com/redis/go-redis/v9 v9.17.3
	github.com/tidwall/gjson v1.18.0
	github.com/valllabh/ocsf-schema-golang v1.0.3
	go.uber.org/zap v1.27.1
	golang.org/x/crypto v0.47.0
	golang.org/x/net v0.49.0
	golang.org/x/sync v0.19.0
	rsc.io/binaryregexp v0.2.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-json-experiment/json v0.0.0-20251027170946-4849db3c2f7e // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kaptinlin/go-i18n v0.2.3 // indirect
	github.com/kaptinlin/jsonpointer v0.4.9 // indirect
	github.com/kaptinlin/messageformat-go v0.4.9 // indirect
	github.com/miekg/dns v1.1.57 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/mod v0.31.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.33.0 // indirect
	golang.org/x/tools v0.40.0 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)
