all: interlink vk installer

interlink:
	CGO_ENABLED=0 OOS=linux go build -o bin/interlink

vk:
	CGO_ENABLED=0 OOS=linux go build -o bin/vk cmd/virtual-kubelet/main.go

installer:
	CGO_ENABLED=0 OOS=linux go build -o bin/installer cmd/installer/main.go

clean:
	rm -rf ./bin

