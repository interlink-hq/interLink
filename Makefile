all: interlink vk installer

interlink:
	CGO_ENABLED=0 OOS=linux go build -o bin/interlink

vk:
	CGO_ENABLED=0 OOS=linux go build -o bin/vk cmd/virtual-kubelet/main.go

installer:
	CGO_ENABLED=0 OOS=linux go build -o bin/installer cmd/installer/main.go

clean:
	rm -rf ./bin

dagger_registry_delete:
	docker rm -fv registry || true

test:
	dagger_registry_delete
	docker run -d --rm --name registry -p 5432:5000  registry
	cd ci
	dagger go run go main.go k8s.go
	cd -

