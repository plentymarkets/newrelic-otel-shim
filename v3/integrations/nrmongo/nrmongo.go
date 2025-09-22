package nrmongo

import (
	"go.mongodb.org/mongo-driver/v2/event"
	"go.opentelemetry.io/contrib/instrumentation/go.mongodb.org/mongo-driver/v2/mongo/otelmongo"
)

func NewCommandMonitor(opts ...otelmongo.Option) *event.CommandMonitor {
	return otelmongo.NewMonitor(opts...)
}
