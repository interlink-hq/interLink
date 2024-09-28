all: interlink vk installer ssh-tunnel

interlink:
	CGO_ENABLED=0 OOS=linux go build -o bin/interlink cmd/interlink/main.go

vk:
	CGO_ENABLED=0 OOS=linux go build -o bin/vk cmd/virtual-kubelet/main.go

installer:
	CGO_ENABLED=0 OOS=linux go build -o bin/installer cmd/installer/main.go

ssh-tunnel:
	CGO_ENABLED=0 OOS=linux go build -o bin/ssh-tunnel cmd/ssh-tunnel/main.go

clean:
	rm -rf ./bin

test:
	dagger call -m ./ci \
    --name my-tests \
    build-images \
    new-interlink \
    test stdout

