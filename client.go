// Public client API; implementation lives in internal/client.
package pumpfun

import (
	sdkClient "github.com/vasyza/pumpfun-sdk/internal/client"
)

type Client = sdkClient.Client
type Options = sdkClient.Options
type RetryPolicy = sdkClient.RetryPolicy

const (
	DefaultAPIBaseURL = sdkClient.DefaultAPIBaseURL
	DefaultRPCURL     = sdkClient.DefaultRPCURL
	DefaultWSURL      = sdkClient.DefaultWSURL
	DefaultTimeout    = sdkClient.DefaultTimeout
	Version           = sdkClient.Version
)

// NewClient checks the settings and creates a read client.
func NewClient(opts Options) (*Client, error) { return sdkClient.NewClient(opts) }
