package ydylconsolesdk

// Client 是 SDK 对外入口，按资源分组（例如 Result）。
type Client struct {
	Result Result
}

func New(baseURL string, opts ...Option) *Client {
	hc := NewHTTPClient(baseURL, opts...)
	return &Client{
		Result: Result{http: hc},
	}
}
