package valkey

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	inner *redis.Client
}

func NewClient(addr string) *Client {
	opts, err := redis.ParseURL(addr)
	if err != nil {
		opts = &redis.Options{Addr: "localhost:6379"}
	}
	return &Client{inner: redis.NewClient(opts)}
}

func (c *Client) Close() error {
	return c.inner.Close()
}

func (c *Client) SetPosition(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.inner.Set(ctx, key, value, ttl).Err()
}

func (c *Client) GetPosition(ctx context.Context, key string) (string, error) {
	return c.inner.Get(ctx, key).Result()
}
