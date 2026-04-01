all: interlink vk installer ssh-tunnel

interlink:
	CGO_ENABLED=0 OOS=linux go build -o bin/interlink cmd/interlink/main.go cmd/interlink/cri.go

vk:
	CGO_ENABLED=0 OOS=linux go build -o bin/vk cmd/virtual-kubelet/main.go

installer:
	CGO_ENABLED=0 OOS=linux go build -o bin/installer cmd/installer/main.go

ssh-tunnel:
	CGO_ENABLED=0 OOS=linux go build -o bin/ssh-tunnel cmd/ssh-tunnel/main.go

openapi:
	go run cmd/openapi-gen/main.go

clean:
	rm -rf ./bin

# Code quality checks
lint:
	golangci-lint run -v --timeout=30m

unit-test:
	go test -v -race -coverprofile=coverage.out -covermode=atomic ./pkg/...

# Run all checks before integration tests
check: lint unit-test
	@echo "All checks passed!"

test:
	dagger call -m ./ci \
    --name my-tests \
    build-images \
    new-interlink \
    test stdout

test-tls:
	dagger call -m ./ci \
    --name my-mtls-tests \
    build-images \
    new-interlink-mtls \
    test stdout

# Quick local integration test
test-local: all
	@echo "Running local integration test..."
	@./scripts/local-test.sh

# K3s-based integration tests (individual steps)
test-k3s-setup:
	@echo "Setting up K3s test environment..."
	@bash ./scripts/k3s-test-setup.sh

test-k3s-run:
	@echo "Running K3s integration tests..."
	@bash ./scripts/k3s-test-run.sh

test-k3s-cleanup:
	@echo "Cleaning up K3s test environment..."
	@bash ./scripts/k3s-test-cleanup.sh

# Complete K3s integration test cycle
test-k3s: test-k3s-setup test-k3s-run test-k3s-cleanup

