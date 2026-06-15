package client

import (
	"context"
	"fmt"
	"net/http"
)

func (c *Client) ListPorts(ctx context.Context) (PortPool, error) {
	var out PortPool
	err := c.do(ctx, http.MethodGet, "/api/v1/ports", nil, &out)
	return out, err
}

func (c *Client) AllocatePort(ctx context.Context, owner, label string) (AllocateResult, error) {
	var out AllocateResult
	body := map[string]string{"owner": owner, "label": label}
	err := c.do(ctx, http.MethodPost, "/api/v1/ports/allocate", body, &out)
	return out, err
}

func (c *Client) ReturnPort(ctx context.Context, port int) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/ports/%d", port), nil, nil)
}
