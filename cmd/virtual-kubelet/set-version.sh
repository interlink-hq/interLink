#!/bin/bash

if [ -n ${KUBELET_VERSION} ]; then
cat << EOF > pkg/virtualkubelet/version.go
package main

var (
	kubeletVersion = "$KUBELET_VERSION"
)
EOF
fi
