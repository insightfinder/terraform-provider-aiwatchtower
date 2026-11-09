.PHONY: build install test test-unit test-acc testacc test-coverage fmt lint docs clean deps

default: build

# Build the provider
build:
	go build -o terraform-provider-aiwatchtower

# Install the provider locally for testing
install: build
	mkdir -p ~/.terraform.d/plugins/registry.terraform.io/insightfinder/aiwatchtower/1.0.0/linux_amd64
	cp terraform-provider-aiwatchtower ~/.terraform.d/plugins/registry.terraform.io/insightfinder/aiwatchtower/1.0.0/linux_amd64/

# Run unit tests
test: test-unit

test-unit:
	go test -v -short ./internal/provider ./internal/provider/client

# Run acceptance tests (requires a reachable AI WatchTower and AW_* credentials)
test-acc:
	TF_ACC=1 go test -v ./internal/provider -timeout 120m

testacc: test-acc

test-coverage:
	go test -v -cover -coverprofile=coverage.out ./internal/provider ./internal/provider/client
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Format code
fmt:
	go fmt ./...
	terraform fmt -recursive ./examples/

lint:
	golangci-lint run

# Generate documentation
docs:
	go generate

clean:
	rm -f terraform-provider-aiwatchtower
	rm -rf dist/
	rm -f coverage.out coverage.html

deps:
	go mod download
	go mod tidy
