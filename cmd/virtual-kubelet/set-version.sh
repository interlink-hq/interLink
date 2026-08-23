#!/bin/bash

if [ -n ${KUBELET_VERSION} ]; then
cat << EOF > pkg/virtualkubelet/version.go
package virtualkubelet

var (
	KubeletVersion = "$KUBELET_VERSION"
)
EOF
cat << EOF > pkg/interlink/version.go
package interlink

var Version = "$KUBELET_VERSION"
EOF
fi
