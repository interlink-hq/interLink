package api

import (
	"context"

	"github.com/intertwin-eu/interlink/pkg/interlink"
)

type InterLinkHandler struct {
	Config interlink.InterLinkConfig
	Ctx    context.Context
}
