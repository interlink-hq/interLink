docker run -d --name plugin -p 4000:4000 --privileged -v ./example/plugin-config.yaml:/etc/interlink/InterLinkConfig.yaml -e SHARED_FS=true -e SLURMCONFIGPATH=/etc/interlink/InterLinkConfig.yaml ghcr.io/interlink-hq/interlink-sidecar-slurm/interlink-sidecar-slurm:0.4.0

docker run -d -p 3000:3000 --name interlink -v ./example/interlink-config.yaml:/etc/interlink/InterLinkConfig.yaml -e INTERLINKCONFIGPATH=/etc/interlink/InterLinkConfig.yaml ghcr.io/interlink-hq/interlink/interlink:0.4.0

export NODENAME=virtual-kubelet
export KUBELET_PORT=10251
export KUBELET_URL=0.0.0.0
export POD_IP=192.168.5.2
export CONFIGPATH=$PWD/ci/manifests/virtual-kubelet-local.yaml
export KUBECONFIG=~/.kube/config
export KUBELET_URL=0.0.0.0

dlv debug ./cmd/virtual-kubelet/main.go
