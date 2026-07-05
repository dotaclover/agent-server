package mcp

import (
	"go-agent-studio/common"
	mcpservice "go-agent-studio/services/mcp"
	"net/http"

	"github.com/labstack/echo/v4"
)

type Handler struct {
	manager *mcpservice.Manager
}

func NewHandler(manager *mcpservice.Manager) *Handler {
	return &Handler{manager: manager}
}

func (h *Handler) RegisterRoutes(g *echo.Group) {
	g.GET("/status", h.Status)
}

func (h *Handler) Status(c echo.Context) error {
	if h == nil || h.manager == nil {
		return common.Fail(c, http.StatusServiceUnavailable, "mcp manager is not configured")
	}
	return common.Success(c, "mcp status", h.manager.Status())
}
