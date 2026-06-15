package client

import (
	"context"
	"net/http"
)

func (c *Client) ListCAs(ctx context.Context) ([]string, error) {
	var out []string
	err := c.do(ctx, http.MethodGet, "/api/v1/cas", nil, &out)
	return out, err
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var out Status
	err := c.do(ctx, http.MethodGet, "/api/v1/status", nil, &out)
	return out, err
}
