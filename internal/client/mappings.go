package client

import (
	"context"
	"fmt"
	"net/http"
)

// MappingRequest is the create/replace body (§12). ListenPort/ListenTLS are
// pointers so an omitted field uses the server default (port 443, tls true).
type MappingRequest struct {
	ListenPort *int             `json:"listen_port,omitempty"`
	ListenTLS  *bool            `json:"listen_tls,omitempty"`
	Upstream   *UpstreamRequest `json:"upstream,omitempty"`
	Allocate   bool             `json:"allocate,omitempty"`
	Label      string           `json:"label,omitempty"`
}

type UpstreamRequest struct {
	Scheme string             `json:"scheme"`
	Host   string             `json:"host"`
	Port   int                `json:"port"`
	TLS    UpstreamTLSRequest `json:"tls"`
}

type UpstreamTLSRequest struct {
	Mode   string `json:"mode,omitempty"`
	CAName string `json:"ca_name,omitempty"`
}

// PutMapping creates (replace=false → POST) or replaces (replace=true → PUT
// /{listen_port}) a mapping. When replace is true the request must carry a
// ListenPort (used for the path and validated by the server against the body).
//
// AIDEV-NOTE: the same req is sent as the body for both POST (create) and PUT
// (replace); the server ignores listen_port in the POST body and validates it
// against the path port on PUT.
func (c *Client) PutMapping(ctx context.Context, lb string, req MappingRequest, replace bool) (Mapping, error) {
	var out Mapping
	if !replace {
		err := c.do(ctx, http.MethodPost, "/api/v1/lbs/"+lb+"/mappings", req, &out)
		return out, err
	}
	if req.ListenPort == nil {
		return out, fmt.Errorf("client: replace requires a listen port")
	}
	path := fmt.Sprintf("/api/v1/lbs/%s/mappings/%d", lb, *req.ListenPort)
	err := c.do(ctx, http.MethodPut, path, req, &out)
	return out, err
}

func (c *Client) DeleteMapping(ctx context.Context, lb string, port int) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/lbs/%s/mappings/%d", lb, port), nil, nil)
}
