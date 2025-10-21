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

# Integration tests with ephemeral K3s cluster (no Dagger required)
# Runs lint and unit tests first
test-k3s: check test-k3s-setup test-k3s-run test-k3s-cleanup

test-k3s-setup:
	@echo "Setting up ephemeral K3s cluster for integration tests..."
	@./scripts/k3s-test-setup.sh

test-k3s-run:
	@echo "Running integration tests on K3s..."
	@./scripts/k3s-test-run.sh

test-k3s-cleanup:
	@echo "Cleaning up K3s test environment..."
	@./scripts/k3s-test-cleanup.sh

# Quick local integration test
test-local: all
	@echo "Running local integration test..."
	@./scripts/local-test.sh

