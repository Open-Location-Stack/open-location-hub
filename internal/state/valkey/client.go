package valkey

import (
	"context"
	"errors"
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

func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.inner.Set(ctx, key, value, ttl).Err()
}

func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := c.inner.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return value, err
}

func (c *Client) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return c.inner.SetNX(ctx, key, value, ttl).Result()
}

func (c *Client) Delete(ctx context.Context, key string) error {
	return c.inner.Del(ctx, key).Err()
}
