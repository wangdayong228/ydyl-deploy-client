package ydylconsolesdk

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/nft-rainbow/rainbow-goutils/utils/ginutils"
)

// HTTPClient 是最原生的 HTTP 交互层：负责 resty client、通用请求、错误解析。
type HTTPClient struct {
	http *resty.Client
}

type Option func(*HTTPClient)

// NewHTTPClient 创建底层 HTTP 客户端。
func NewHTTPClient(baseURL string, opts ...Option) *HTTPClient {
	baseURL = strings.TrimRight(baseURL, "/")

	rc := resty.New().
		SetBaseURL(baseURL).
		SetHeader("Accept", "application/json").
		SetTimeout(30 * time.Second)

	c := &HTTPClient{http: rc}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

func WithRestyClient(rc *resty.Client) Option {
	return func(c *HTTPClient) {
		if rc != nil {
			c.http = rc
		}
	}
}

func WithBearerToken(token string) Option {
	return func(c *HTTPClient) {
		if token != "" {
			c.http.SetAuthToken(token)
		}
	}
}

func WithHeader(key, value string) Option {
	return func(c *HTTPClient) {
		if key != "" {
			c.http.SetHeader(key, value)
		}
	}
}

// APIError 表示服务端返回的 ginutils.GinErrorBody（HTTP status 可能是 400/409/599 等）。
type APIError struct {
	StatusCode int
	Body       ginutils.GinErrorBody
}

func (e *APIError) Error() string {
	return fmt.Sprintf("ydyl-console-service api error: status=%d code=%d message=%q", e.StatusCode, e.Body.Code, e.Body.Message)
}

func (c *HTTPClient) get(ctx context.Context, path string, out any) error {
	ge := new(ginutils.GinErrorBody)
	resp, err := c.http.R().
		SetContext(ctx).
		SetResult(out).
		SetError(ge).
		Get(path)
	if err != nil {
		return err
	}
	if resp.IsError() {
		// 可能存在非 GinErrorBody 的错误响应，这里尽量返回原始内容便于排查
		if ge.Message == "" && resp.Body() != nil {
			ge.Message = string(resp.Body())
		}
		return &APIError{StatusCode: resp.StatusCode(), Body: *ge}
	}
	return nil
}

