package synchronizer

import (
	"context"
	"encoding/json"
	"fmt"
	"glouton/bleemeo/client"
	"time"
)

type wrapperClient struct {
	s                *Synchronizer
	client           *client.HTTPClient
	duplicateError   error
	duplicateChecked bool
}

func (cl *wrapperClient) ThrottleDeadline() time.Time {
	if cl == nil {
		return time.Time{}
	}

	return cl.client.ThrottleDeadline()
}

func (cl *wrapperClient) Do(ctx context.Context, method string, path string, params map[string]string, data interface{}, result interface{}) (statusCode int, err error) {
	if cl == nil {
		return 0, fmt.Errorf("%w: HTTP client", errUninitialized)
	}

	if !cl.duplicateChecked {
		cl.duplicateChecked = true
		cl.duplicateError = cl.s.checkDuplicated()
	}

	if cl.duplicateError != nil {
		return 0, cl.duplicateError
	}

	return cl.client.Do(ctx, method, path, params, data, result)
}

func (cl *wrapperClient) Iter(ctx context.Context, resource string, params map[string]string) ([]json.RawMessage, error) {
	if cl == nil {
		return nil, fmt.Errorf("%w: HTTP client", errUninitialized)
	}

	if !cl.duplicateChecked {
		cl.duplicateChecked = true
		cl.duplicateError = cl.s.checkDuplicated()
	}

	if cl.duplicateError != nil {
		return nil, cl.duplicateError
	}

	return cl.client.Iter(ctx, resource, params)
}
