package ankitts

import (
	"context"
	"errors"

	"jlzhjp.dev/ankitts/pipeline"
)

// RetryClassifier is optionally implemented by application components that can
// distinguish transient operation failures from permanent ones.
type RetryClassifier interface {
	ShouldRetry(error) bool
}

type permanentOperationError struct{ err error }

func (e *permanentOperationError) Error() string { return e.err.Error() }
func (e *permanentOperationError) Unwrap() error { return e.err }

func permanentOperationFailure(err error) error {
	if err == nil {
		return nil
	}
	return &permanentOperationError{err: err}
}

func componentRetryPredicate(component any) pipeline.RetryPredicate {
	classifier, _ := component.(RetryClassifier)
	return func(err error) bool {
		var permanent *permanentOperationError
		if errors.As(err, &permanent) || errors.Is(err, context.Canceled) {
			return false
		}
		if classifier != nil {
			return classifier.ShouldRetry(err)
		}
		return true
	}
}
