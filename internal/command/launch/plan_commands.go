package launch

import (
	"context"

	"github.com/spf13/cobra"
	"github.com/superfly/flyctl/internal/command"
	"github.com/superfly/flyctl/internal/command/launch/plan"
	"github.com/superfly/flyctl/internal/flag"
)

func NewPlan() *cobra.Command {
	const desc = `[experimental] Granular subcommands for creating and configuring apps`

	cmd := command.New("plan", desc, desc, nil, command.RequireSession, command.LoadAppConfigIfPresent)
	cmd.Args = cobra.NoArgs

	cmd.AddCommand(newPropose())
	cmd.AddCommand(newCreate())
	cmd.AddCommand(newPostgres())
	cmd.AddCommand(newRedis())
	cmd.AddCommand(newTigris())
	cmd.AddCommand(newGenerate())

	// Don't advertise this command yet
	cmd.Hidden = true

	return cmd
}

func newPropose() *cobra.Command {
	const desc = "[experimental] propose a plan based on scanning the source code or Dockerfile"
	cmd := command.New("propose", desc, desc, runPropose, command.LoadAppConfigIfPresent)

	flag.Add(cmd,
		flag.Region(),
		flag.Org(),
		flag.String{
			Name:        "from",
			Description: "A github repo URL to use as a template for the new app",
		},
		flag.Bool{
			Name:        "no-create",
			Description: "Don't create an app",
			Default:     true,
			Hidden:      true,
		},
		flag.Bool{
			Name:        "manifest",
			Description: "Output the proposed manifest",
			Default:     true,
			Hidden:      true,
		},
		flag.String{
			Name:        "name",
			Description: `Name of the new app`,
		},
		flag.Bool{
			Name:   "force-name",
			Hidden: true,
		},
		flag.Bool{
			Name:        "copy-config",
			Description: "Use the configuration file if present without prompting",
			Default:     false,
		},
		flag.String{
			Name:        "manifest-path",
			Description: "Path to write the manifest to",
			Default:     "",
			Hidden:      true,
		},
		flag.Bool{
			Name:        "no-blank",
			Description: "Don't allow a \"blank\" app (nothing could be detected)",
			Default:     true,
		},
	)

	return cmd
}

func newCreate() *cobra.Command {
	const desc = "[experimental] create application"
	cmd := command.New("create", desc, desc, runCreate)
	cmd.Args = cobra.ExactArgs(1)

	flag.Add(cmd,
		flag.String{
			Name:        "from-manifest",
			Description: "Path to read the manifest from",
			Default:     "",
			Hidden:      true,
		},
	)

	return cmd
}

func newPostgres() *cobra.Command {
	const desc = "[experimental] create postgres database"
	cmd := command.New("postgres", desc, desc, runPostgres)
	cmd.Args = cobra.ExactArgs(1)

	flag.Add(cmd,
		flag.String{
			Name:        "from-manifest",
			Description: "Path to read the manifest from",
			Default:     "",
			Hidden:      true,
		},
	)

	return cmd
}

func newRedis() *cobra.Command {
	const desc = "[experimental] create redis database"
	cmd := command.New("redis", desc, desc, runRedis)
	cmd.Args = cobra.ExactArgs(1)

	flag.Add(cmd,
		flag.String{
			Name:        "from-manifest",
			Description: "Path to read the manifest from",
			Default:     "",
			Hidden:      true,
		},
	)

	return cmd
}

func newTigris() *cobra.Command {
	const desc = "[experimental] create tigris database"
	cmd := command.New("tigris", desc, desc, runTigris)
	cmd.Args = cobra.ExactArgs(1)

	flag.Add(cmd,
		flag.String{
			Name:        "from-manifest",
			Description: "Path to read the manifest from",
			Default:     "",
			Hidden:      true,
		},
	)

	return cmd
}

func newGenerate() *cobra.Command {
	const desc = "[experimental] generate Dockerfile and other configuration files based on the plan"
	cmd := command.New("generate", desc, desc, runGenerate)
	cmd.Args = cobra.ExactArgs(1)

	flag.Add(cmd,
		// flag.App(),
		// flag.Region(),
		// flag.Org(),
		// flag.AppConfig(),
		flag.Bool{
			Name:        "no-deploy",
			Description: "Don't deploy the app",
			Default:     true,
			Hidden:      true,
		},
		flag.String{
			Name:        "from-manifest",
			Description: "Path to read the manifest from",
			Default:     "",
			Hidden:      true,
		},
	)

	return cmd
}

func RunPlan(ctx context.Context, step string) error {
	ctx = context.WithValue(ctx, plan.PlanStepKey, step)
	return run(ctx)
}

func runPropose(ctx context.Context) error {
	return RunPlan(ctx, "propose")
}

func runCreate(ctx context.Context) error {
	flag.SetString(ctx, "from-manifest", flag.FirstArg(ctx))
	RunPlan(ctx, "create")
	return nil
}

func runPostgres(ctx context.Context) error {
	flag.SetString(ctx, "from-manifest", flag.FirstArg(ctx))
	RunPlan(ctx, "postgres")
	return nil
}

func runRedis(ctx context.Context) error {
	flag.SetString(ctx, "from-manifest", flag.FirstArg(ctx))
	RunPlan(ctx, "redis")
	return nil
}

func runTigris(ctx context.Context) error {
	flag.SetString(ctx, "from-manifest", flag.FirstArg(ctx))
	RunPlan(ctx, "tigris")
	return nil
}

func runGenerate(ctx context.Context) error {
	flag.SetString(ctx, "from-manifest", flag.FirstArg(ctx))
	return RunPlan(ctx, "generate")
}
