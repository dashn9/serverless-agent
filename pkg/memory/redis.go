package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type FunctionConfig struct {
	Name                   string
	Handler                string
	Resources              ResourceLimits
	Timeout                int32
	MaxConcurrency         int32
	MaxConcurrencyBehavior string
	Env                    map[string]string
}

type ResourceLimits struct {
	CPU    int32
	Memory int64
}

type RedisMemory struct {
	client *redis.Client
	ctx    context.Context
}

func NewRedisMemory(addr string) *RedisMemory {
	opt, err := redis.ParseURL(addr)
	if err != nil {
		panic(err)
	}

	client := redis.NewClient(opt)

	return &RedisMemory{
		client: client,
		ctx:    context.Background(),
	}
}

func (r *RedisMemory) Close() error {
	return r.client.Close()
}

func (r *RedisMemory) SaveFunction(config *FunctionConfig) error {
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("agent:function:%s", config.Name)
	return r.client.Set(r.ctx, key, data, 0).Err()
}

func (r *RedisMemory) GetFunction(name string) (*FunctionConfig, error) {
	key := fmt.Sprintf("agent:function:%s", name)
	data, err := r.client.Get(r.ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}

	var config FunctionConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func (r *RedisMemory) AppendExecutionLog(executionID, chunk string) error {
	key := fmt.Sprintf("flux:exec-logs:%s", executionID)
	pipe := r.client.Pipeline()
	pipe.Append(r.ctx, key, chunk)
	pipe.Expire(r.ctx, key, time.Hour)
	_, err := pipe.Exec(r.ctx)
	return err
}

func (r *RedisMemory) GetAllFunctions() ([]*FunctionConfig, error) {
	keys, err := r.client.Keys(r.ctx, "agent:function:*").Result()
	if err != nil {
		return nil, err
	}

	functions := make([]*FunctionConfig, 0, len(keys))
	for _, key := range keys {
		data, err := r.client.Get(r.ctx, key).Bytes()
		if err != nil {
			continue
		}

		var config FunctionConfig
		if err := json.Unmarshal(data, &config); err != nil {
			continue
		}

		functions = append(functions, &config)
	}

	return functions, nil
}
