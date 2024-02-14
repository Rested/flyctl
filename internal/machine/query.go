package machine

import (
	"context"

	"github.com/samber/lo"
	fly "github.com/superfly/fly-go"
	"github.com/superfly/fly-go/flaps"
)

func ListActive(ctx context.Context) ([]*fly.Machine, error) {
	flapsClient := flaps.FromContext(ctx)

	machines, err := flapsClient.List(ctx, "")
	if err != nil {
		return nil, err
	}

	machines = lo.Filter(machines, func(m *fly.Machine, _ int) bool {
		return m.Config != nil && m.IsActive() && !m.IsReleaseCommandMachine() && !m.IsFlyAppsConsole()
	})

	return machines, nil
}
