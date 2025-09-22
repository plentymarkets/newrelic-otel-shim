package nrgorilla

import (
	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
)

func Middleware(service string, opts ...otelmux.Option) mux.MiddlewareFunc {
	return otelmux.Middleware(service, opts...)
}
