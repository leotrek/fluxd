.PHONY: build test test-race test-cover vet fmt fmt-check lint benchmark cross clean
build:
	mkdir -p bin
	go build -trimpath -o bin/fluxd ./cmd/fluxd
	go build -trimpath -o bin/fluxctl ./cmd/fluxctl
	go build -trimpath -o bin/fluxbench ./cmd/fluxbench
test:
	go test ./...
test-race:
	go test -race ./...
test-cover:
	go test -coverprofile=coverage.out ./...
vet:
	go vet ./...
fmt:
	gofmt -w cmd internal examples dev
fmt-check:
	@test -z "$$(gofmt -l cmd internal examples dev)" || (gofmt -l cmd internal examples dev; exit 1)
lint: fmt-check vet
	@echo "Lightweight lint: gofmt and go vet; no external linter dependency."
benchmark:
	go test -run '^$$' -bench . -benchmem ./internal/acquisition ./internal/compression/...
	go run ./cmd/fluxbench -bytes 67108864 -iterations 3 > benchmark.jsonl
cross:
	mkdir -p bin/linux-amd64 bin/linux-arm64 bin/linux-armv7
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/linux-amd64/ ./cmd/...
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/linux-arm64/ ./cmd/...
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o bin/linux-armv7/ ./cmd/...
clean:
	rm -rf bin coverage.out benchmark.jsonl
