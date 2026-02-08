BINARY=claude-autopilot
VERSION?=0.1.0
GOFLAGS=-ldflags "-X main.version=$(VERSION)"

.PHONY: build install test test-short integration smoke ci clean release lint

build:
	go build $(GOFLAGS) -o $(BINARY) .

install:
	go install $(GOFLAGS) .

test:
	go test ./... -v -count=1

test-short:
	go test ./... -short -count=1

integration:
	bash test/integration.sh

ci: test smoke integration

lint:
	go vet ./...

clean:
	rm -f $(BINARY)
	rm -rf dist/

release: clean
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -o dist/$(BINARY)-darwin-arm64 .
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -o dist/$(BINARY)-darwin-amd64 .
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o dist/$(BINARY)-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -o dist/$(BINARY)-linux-arm64 .
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o dist/$(BINARY)-windows-amd64.exe .

smoke: build
	bash test/smoke.sh
