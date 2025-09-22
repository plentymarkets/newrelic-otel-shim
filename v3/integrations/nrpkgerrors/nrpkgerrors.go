package nrpkgerrors

import "errors"

var (
	errorNil = errors.New("nrpkgerrors: nil error")
)

func Wrap(e error) error {
	if e == nil {
		return errorNil
	}
	return e
}
