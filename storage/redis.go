package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrCacheMiss = errors.New("cache miss")

type RedisCache struct {
	client *redis.Client
	ctx    context.Context
}

func NewRedisCache(redisURL string) (*RedisCache, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, err
	}
	return &RedisCache{client: client, ctx: ctx}, nil
}

func (r *RedisCache) SetMetadata(metadata *DemoMetadata, ttl time.Duration) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return r.client.Set(r.ctx, "demo:metadata:"+metadata.DemoID, data, ttl).Err()
}

func (r *RedisCache) GetMetadata(demoID string) (*DemoMetadata, error) {
	data, err := r.client.Get(r.ctx, "demo:metadata:"+demoID).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrCacheMiss
	}
	if err != nil {
		return nil, err
	}
	var metadata DemoMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

// SetMatchIndex stores a matchID → demoID mapping with a TTL.
func (r *RedisCache) SetMatchIndex(matchID, demoID string, ttl time.Duration) error {
	return r.client.Set(r.ctx, "demo:match:"+matchID, demoID, ttl).Err()
}

// GetMatchIndex returns the demoID for a given matchID.
func (r *RedisCache) GetMatchIndex(matchID string) (string, error) {
	demoID, err := r.client.Get(r.ctx, "demo:match:"+matchID).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrCacheMiss
	}
	return demoID, err
}

func (r *RedisCache) DeleteMetadata(demoID string) {
	r.client.Del(r.ctx, "demo:metadata:"+demoID)
}
