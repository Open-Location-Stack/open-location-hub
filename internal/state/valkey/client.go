package valkey

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps a Valkey-compatible Redis client used for transient hub state.
type Client struct {
	inner *redis.Client
}

// NewClient constructs a Client from a Redis URL and falls back to the local
// default if parsing fails.
func NewClient(addr string) *Client {
	opts, err := redis.ParseURL(addr)
	if err != nil {
		opts = &redis.Options{Addr: "localhost:6379"}
	}
	return &Client{inner: redis.NewClient(opts)}
}

// Close closes the underlying Redis client.
func (c *Client) Close() error {
	return c.inner.Close()
}

// SetPosition stores a string value with a TTL.
func (c *Client) SetPosition(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.inner.Set(ctx, key, value, ttl).Err()
}

// GetPosition loads a string value.
func (c *Client) GetPosition(ctx context.Context, key string) (string, error) {
	return c.inner.Get(ctx, key).Result()
}

// Set stores a byte payload with a TTL.
func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.inner.Set(ctx, key, value, ttl).Err()
}

// Get loads a byte payload, returning nil when the key is absent.
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := c.inner.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return value, err
}

// SetNX stores a byte payload only when the key does not yet exist.
func (c *Client) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return c.inner.SetNX(ctx, key, value, ttl).Result()
}

// Delete removes a key from the cache.
func (c *Client) Delete(ctx context.Context, key string) error {
	return c.inner.Del(ctx, key).Err()
}
