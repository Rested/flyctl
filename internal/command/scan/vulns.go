package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/superfly/flyctl/internal/appconfig"
	"github.com/superfly/flyctl/internal/command"
	"github.com/superfly/flyctl/internal/flag"
	"github.com/superfly/flyctl/internal/flyutil"
	"github.com/superfly/flyctl/iostreams"
)

func newVulns() *cobra.Command {
	const (
		usage = "vulns <vulnid> ... [flags]"
		short = "Scan an application's image for vulnerabilities"
		long  = "Generate a text or JSON report of vulnerabilities found in a application's image.\n" +
			"If a machine is selected the image from that machine is scanned. Otherwise the image\n" +
			"of the first running machine is scanned. When a severity is specified, any vulnerabilities\n" +
			"less than the severity are omitted. When vulnIds are specified, any vulnerability not\n" +
			"in the vulnID list is omitted."
	)
	cmd := command.New(usage, short, long, runVulns,
		command.RequireSession,
		command.RequireAppName,
	)

	cmd.Args = cobra.ArbitraryArgs
	flag.Add(
		cmd,
		flag.App(),
		flag.Bool{
			Name:        "json",
			Description: "Output the scan results in JSON format",
		},
		flag.String{
			Name:        "image",
			Shorthand:   "i",
			Description: "Scan the repository image",
		},
		flag.String{
			Name:        "machine",
			Shorthand:   "m",
			Description: "Scan the image of the machine with the specified ID",
		},
		flag.Bool{
			Name:        "select",
			Shorthand:   "s",
			Description: "Select which machine to scan the image of from a list",
			Default:     false,
		},
		flag.String{
			Name:        "severity",
			Shorthand:   "S",
			Description: fmt.Sprintf("Report only issues with a specific severity %v", allowedSeverities),
		},
	)

	return cmd
}

func runVulns(ctx context.Context) error {
	filter, err := argsGetVulnFilter(ctx)
	if err != nil {
		return err
	}

	if flag.IsSpecified(ctx, "json") && filter.IsSpecified() {
		return fmt.Errorf("filtering by severity or CVE is not supported when outputting JSON")
	}

	apiClient := flyutil.ClientFromContext(ctx)
	appName := appconfig.NameFromContext(ctx)
	app, err := apiClient.GetAppCompact(ctx, appName)
	if err != nil {
		return fmt.Errorf("failed to get app: %w", err)
	}

	imgPath, err := argsGetImgPath(ctx, app)
	if err != nil {
		return err
	}

	token, err := makeScantronToken(ctx, app.Organization.ID, app.ID)
	if err != nil {
		return err
	}

	res, err := scantronVulnscanReq(ctx, imgPath, token)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("failed fetching scan data (status code %d)", res.StatusCode)
	}

	if flag.GetBool(ctx, "json") {
		ios := iostreams.FromContext(ctx)
		if _, err := io.Copy(ios.Out, res.Body); err != nil {
			return fmt.Errorf("failed to read scan results: %w", err)
		}
		return nil
	}

	scan := &Scan{}
	if err = json.NewDecoder(res.Body).Decode(scan); err != nil {
		return fmt.Errorf("failed to read scan results: %w", err)
	}
	if scan.SchemaVersion != 2 {
		return fmt.Errorf("scan result has the wrong schema")
	}

	scan = filterScan(scan, filter)
	return presentScan(ctx, scan)
}

func presentScan(ctx context.Context, scan *Scan) error {
	ios := iostreams.FromContext(ctx)

	// TODO: scan.Metadata?
	fmt.Fprintf(ios.Out, "Report created at: %s\n", scan.CreatedAt)
	for _, res := range scan.Results {
		fmt.Fprintf(ios.Out, "Target %s: %s\n", res.Type, res.Target)
		for _, vuln := range res.Vulnerabilities {
			fmt.Fprintf(ios.Out, "  %s %s: %s %s\n", vuln.Severity, vuln.VulnerabilityID, vuln.PkgName, vuln.InstalledVersion)
		}
	}
	return nil
}
