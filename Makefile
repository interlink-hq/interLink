all: interlink vk sidecars installer

interlink:
	CGO_ENABLED=0 OOS=linux go build -o bin/interlink cmd/interlink/main.go

vk:
	CGO_ENABLED=0 OOS=linux go build -o bin/vk

sidecars:
	CGO_ENABLED=0 GOOS=linux go build -o bin/docker-sd cmd/sidecars/docker/main.go
	CGO_ENABLED=0 GOOS=linux go build -o bin/slurm-sd cmd/sidecars/slurm/main.go

installer:
	CGO_ENABLED=0 OOS=linux go build -o bin/interlink cmd/installer/main.go

clean:
	rm -rf ./bin

all_ppc64le:
	CGO_ENABLED=0 GOOS=linux GOARCH=ppc64le go build -o bin/interlink cmd/interlink/main.go
	CGO_ENABLED=0 GOOS=linux GOARCH=ppc64le go build -o bin/vk
	CGO_ENABLED=0 GOOS=linux GOARCH=ppc64le go build -o bin/docker-sd cmd/sidecars/docker/main.go
	CGO_ENABLED=0 GOOS=linux GOARCH=ppc64le go build -o bin/slurm-sd cmd/sidecars/slurm/main.go

start_interlink_slurm:
	./bin/interlink &> ./logs/interlink.log &
	./bin/slurm-sd  &> ./logs/slurm-sd.log &

start_interlink_docker:
	./bin/interlink &> ./logs/interlink.log &
	./bin/docker-sd  &> ./logs/docker-sd.log &

