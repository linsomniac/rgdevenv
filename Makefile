.PHONY: build test race vet fmt lint
build:
	go build ./...
test:
	go test ./...
race:
	go test ./... -race
vet:
	go vet ./...
fmt:
	gofmt -w .
lint:
	golangci-lint run
