package interlink

import (
	"context"

	"k8s.io/client-go/kubernetes"
)

var Ctx context.Context
var Clientset *kubernetes.Clientset
