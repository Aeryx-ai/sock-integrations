module github.com/aeryx-ai/sock-integrations/runner/sock-codex-inject

go 1.26.9

require (
	github.com/coder/websocket v1.8.14
	github.com/guygrigsby/sock-client v0.0.0
)

require (
	connectrpc.com/connect v1.21.0 // indirect
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/guygrigsby/sock-contract v0.5.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/guygrigsby/sock-client => ../../../client

replace github.com/guygrigsby/sock-contract => ../../../contract
