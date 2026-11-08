.PHONY: build flash gdb rtt install-pack disassembly new hub test

build:
	go run ./cmd/dev node build --name relay

flash:
	go run ./cmd/dev node build --name relay --flash --rtt

disassembly:
	go run ./cmd/dev node build --name relay --disassembly

gdb rtt install-pack:
	go run ./cmd/dev node $@ --name relay

new:
	go run ./cmd/dev node new

hub:
	go run ./cmd/dev hub --diagnostics rf

test:
	go test ./...
