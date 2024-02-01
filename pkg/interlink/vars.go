package interlink

import (
	"context"

	"github.com/intertwin-eu/interlink/pkg/common"
)

var Ctx context.Context

type InterLinkHandler struct {
	Config common.InterLinkConfig
}
