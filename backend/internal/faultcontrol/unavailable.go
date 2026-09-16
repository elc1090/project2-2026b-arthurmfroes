package faultcontrol

import (
	"context"
	"fmt"
)

// UnavailableDriver keeps the HTTP contract explicit while an environment-specific
// driver has not passed its prerequisite checks. It never simulates an action.
type UnavailableDriver struct {
	Reason error
}

func (d UnavailableDriver) Ready() error {
	if d.Reason != nil {
		return d.Reason
	}
	return ErrDriverUnavailable
}

func (d UnavailableDriver) Apply(context.Context, Operation, ResolvedTarget) error {
	return fmt.Errorf("%w", d.Ready())
}
