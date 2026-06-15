package client

import (
	"context"
	"net/http"
)

func (c *Client) ListLBs(ctx context.Context) ([]LoadBalancer, error) {
	var out []LoadBalancer
	if err := c.do(ctx, http.MethodGet, "/api/v1/lbs", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) CreateLB(ctx context.Context, name, label string) (LoadBalancer, error) {
	var out LoadBalancer
	body := map[string]string{"name": name, "label": label}
	err := c.do(ctx, http.MethodPost, "/api/v1/lbs", body, &out)
	return out, err
}

func (c *Client) GetLB(ctx context.Context, name string) (LoadBalancer, error) {
	var out LoadBalancer
	err := c.do(ctx, http.MethodGet, "/api/v1/lbs/"+name, nil, &out)
	return out, err
}

func (c *Client) SetLBLabel(ctx context.Context, name, label string) (LoadBalancer, error) {
	var out LoadBalancer
	body := map[string]string{"label": label}
	err := c.do(ctx, http.MethodPatch, "/api/v1/lbs/"+name, body, &out)
	return out, err
}

func (c *Client) DeleteLB(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/lbs/"+name, nil, nil)
}
