#!/bin/bash

if [ -n ${KUBELET_VERSION} ]; then
cat << EOF > pkg/virtualkubelet/version.go
package virtualkubelet

var (
	KubeletVersion = "$KUBELET_VERSION"
)
EOF
fi
