package nrlogrus

import (
	"github.com/newrelic/go-agent/v3/newrelic"
	"github.com/sirupsen/logrus"
)

type ContextFormatter struct {
	app       *newrelic.Application
	formatter logrus.Formatter
}

func NewFormatter(app *newrelic.Application, formatter logrus.Formatter) ContextFormatter {
	return ContextFormatter{
		app:       app,
		formatter: formatter,
	}
}


func (f ContextFormatter) Format(e *logrus.Entry) ([]byte, error) {

	txn := newrelic.FromContext(e.Context)
	span := txn.OTelSpan()
	if span != nil {
		e.Data["trace.id"] = span.SpanContext().TraceID().String()
		e.Data["span.id"] = span.SpanContext().SpanID().String()
	}

	return f.formatter.Format(e)
}
