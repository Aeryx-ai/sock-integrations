module github.com/guygrigsby/sock-integrations/test

go 1.26.2

replace github.com/guygrigsby/sock-contract => ../../contract

require (
	connectrpc.com/connect v1.21.0
	github.com/guygrigsby/sock-contract v0.0.0-00010101000000-000000000000
)

require google.golang.org/protobuf v1.36.12 // indirect
