package nrredis

import (
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

func NewHook(opts ...redisotel.Option) {
	panic("use InstrumentTracing instead")
}

func InstrumentTracing(rdb redis.UniversalClient, opts ...redisotel.TracingOption) error {
	return redisotel.InstrumentTracing(rdb, opts...)
}
