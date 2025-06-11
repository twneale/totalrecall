module shelper

go 1.22.4

require (
	github.com/elastic/go-elasticsearch/v8 v8.10.0 // indirect
	totalrecall/pkg/estransport v0.0.0-00010101000000-000000000000
)

require github.com/elastic/elastic-transport-go/v8 v8.0.0-20230329154755-1a3c63de0db6 // indirect

replace totalrecall/pkg/estransport => ../../pkg/estransport
