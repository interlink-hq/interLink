export NODENAME=virtual-kubelet
export KUBELET_PORT=10251
export KUBELET_URL=0.0.0.0
export POD_IP=172.17.0.1
export CONFIGPATH=$PWD/ci/manifests/virtual-kubelet-local.yaml
export KUBECONFIG=~/.kube/config
export KUBELET_URL=0.0.0.0

dlv debug ./cmd/virtual-kubelet/main.go
