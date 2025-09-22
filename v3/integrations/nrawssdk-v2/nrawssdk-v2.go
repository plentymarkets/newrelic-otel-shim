package nrawssdkv2

import (
	"github.com/aws/smithy-go/middleware"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
)

func AppendMiddlewares(apiOptions *[]func(*middleware.Stack) error, opts ...otelaws.Option) {
	otelaws.AppendMiddlewares(apiOptions, opts...)
}
