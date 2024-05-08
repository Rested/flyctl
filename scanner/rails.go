package scanner

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pkg/errors"
	"github.com/superfly/flyctl/internal/command/launch/plan"
	"github.com/superfly/flyctl/internal/flyerr"
)

var healthcheck_channel = make(chan string)
var bundle, ruby string
var binrails = filepath.Join(".", "bin", "rails")

func configureRails(sourceDir string, config *ScannerConfig) (*SourceInfo, error) {
	// `bundle init` will create a file with a commented out rails gem,
	// so checking for that can produce a false positive.  Look for
	// Rails three other ways...
	rails := checksPass(sourceDir+"/bin", fileExists("rails")) ||
		checksPass(sourceDir, dirContains("config.ru", "Rails")) ||
		checksPass(sourceDir, dirContains("Gemfile.lock", " rails "))

	if !rails {
		return nil, nil
	}

	// find absolute pat to bundle, ruby executables
	// see: https://tip.golang.org/doc/go1.19#os-exec-path
	var err error
	bundle, err = exec.LookPath("bundle")
	if err != nil {
		if errors.Is(err, exec.ErrDot) {
			bundle, err = filepath.Abs(bundle)
		}

		if err != nil {
			return nil, errors.Wrap(err, "failure finding bundle executable")
		}
	}

	ruby, err = exec.LookPath("ruby")
	if err != nil {
		if errors.Is(err, exec.ErrDot) {
			ruby, err = filepath.Abs(ruby)
		}

		if err != nil {
			return nil, errors.Wrap(err, "failure finding ruby executable")
		}
	}

	// verify that the bundle will install before proceeding
	args := []string{"install"}

	if checksPass(sourceDir, fileExists("Gemfile.lock")) {
		args = append(args, "--quiet")
	}

	cmd := exec.Command(bundle, args...)
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, errors.Wrap(err, "Failed to install bundle, exiting")
	}

	s := &SourceInfo{
		Family:               "Rails",
		Callback:             RailsCallback,
		FailureCallback:      RailsFailureCallback,
		Port:                 3000,
		ConsoleCommand:       "/rails/bin/rails console",
		AutoInstrumentErrors: true,
	}

	if checksPass(sourceDir, dirContains("Gemfile", "litestack")) {
		// don't prompt for pg, redis if litestack is in the Gemfile
		s.DatabaseDesired = DatabaseKindSqlite
		s.SkipDatabase = true
	} else if checksPass(sourceDir, dirContains("Gemfile", "mysql")) {
		// mysql
		s.DatabaseDesired = DatabaseKindMySQL
		s.SkipDatabase = false
	} else if !checksPass(sourceDir, fileExists("Dockerfile")) || checksPass(sourceDir, dirContains("Dockerfile", "libpq-dev", "postgres")) {
		// postgresql
		s.DatabaseDesired = DatabaseKindPostgres
		s.SkipDatabase = false
	} else {
		// sqlite
		s.DatabaseDesired = DatabaseKindSqlite
		s.SkipDatabase = true
	}

	// enable redis if there are any action cable / anycable channels
	redis := false
	files, err := filepath.Glob("app/channels/*.rb")
	if err == nil && len(files) > 0 {
		redis = true
	}

	// enable redis if redis is used for caching
	prodEnv, err := os.ReadFile("config/environments/production.rb")
	if err == nil && strings.Contains(string(prodEnv), "redis") {
		redis = true
	}

	if redis {
		s.RedisDesired = true
		s.SkipDatabase = false
	}

	// master.key comes with Rails apps from v5.2 onwards, but may not be present
	// if the app does not use Rails encrypted credentials.  Rails v6 added
	// support for multi-environment credentials.  Use the Rails searching
	// sequence for production credentials to determine the RAILS_MASTER_KEY.
	masterKey, err := os.ReadFile("config/credentials/production.key")
	if err != nil {
		masterKey, err = os.ReadFile("config/master.key")
	}

	if err == nil {
		s.Secrets = []Secret{
			{
				Key:   "RAILS_MASTER_KEY",
				Help:  "Secret key for accessing encrypted credentials",
				Value: string(masterKey),
			},
		}
	} else {
		// find absolute path to rake executable
		rake, err := exec.LookPath("rake")
		if err != nil {
			if errors.Is(err, exec.ErrDot) {
				rake, err = filepath.Abs(rake)
			}

			if err != nil {
				return nil, errors.Wrap(err, "failure finding rake executable")
			}
		}

		// support Rails 4 through 5.1 applications, or ones that started out
		// there and never were fully upgraded.
		out, err := exec.Command(rake, "secret").Output()

		if err == nil {
			s.Secrets = []Secret{
				{
					Key:   "SECRET_KEY_BASE",
					Help:  "Secret key used to verify the integrity of signed cookies",
					Value: strings.TrimSpace(string(out)),
				},
			}
		}
	}

	initializersPath := filepath.Join(sourceDir, "config", "initializers")
	if checksPass(initializersPath, dirContains("*.rb", "ENV", "credentials")) {
		s.SkipDeploy = true
		s.DeployDocs = `
Your Rails app is prepared for deployment.

` + config.Colorize.Red(
			`WARNING: One or more of your config initializer files appears to access
environment variables or Rails credentials.  These values generally are not
available during the Docker build process, so you may need to update your
initializers to bypass portions of your setup during the build process.`) + `

More information on what needs to be done can be found at:
https://fly.io/docs/rails/getting-started/existing/#access-to-environment-variables-at-build-time.

Once ready: run 'fly deploy' to deploy your Rails app.
`
	}

	// fetch healthcheck route in a separate thread
	go func() {
		ruby, err := exec.LookPath("ruby")
		if err != nil {
			healthcheck_channel <- ""
			return
		}

		out, err := exec.Command(ruby, binrails, "runner",
			"puts Rails.application.routes.url_helpers.rails_health_check_path").Output()

		if err == nil {
			healthcheck_channel <- strings.TrimSpace(string(out))
		} else {
			healthcheck_channel <- ""
		}
	}()

	return s, nil
}

func RailsCallback(appName string, srcInfo *SourceInfo, plan *plan.LaunchPlan, flags []string) error {
	// Overall strategy: Install and use the dockerfile-rails gem to generate a Dockerfile.
	//
	// If a Dockerfile already exists, run the generator with the --skip flag to avoid overwriting it.
	// This will still do interesting things like update the fly.toml file to add volumes, processes, etc.
	//
	// If the generator fails but a Dockerfile exists, warn the user and proceed.  Only fail if no
	// Dockerfile exists at the end of this process.

	// install dockerfile-rails gem, if not already included and the gem directory is writable
	// if an error occurrs, store it for later in pendingError
	generatorInstalled := false
	var pendingError error
	gemfile, err := os.ReadFile("Gemfile")
	if err != nil {
		return errors.Wrap(err, "Failed to read Gemfile")
	} else if !strings.Contains(string(gemfile), "dockerfile-rails") {
		// check for writable gem installation directory
		writable := false
		out, err := exec.Command("gem", "environment").Output()
		if err == nil {
			regexp := regexp.MustCompile(`INSTALLATION DIRECTORY: (.*)\n`)
			for _, match := range regexp.FindAllStringSubmatch(string(out), -1) {
				// Testing to see if a directory is writable is OS dependent, so
				// we use a brute force method: attempt it and see if it works.
				file, err := os.CreateTemp(match[1], ".flyctl.probe")
				if err == nil {
					writable = true
					file.Close()
					defer os.Remove(file.Name())
				}
			}
		}

		// install dockerfile-rails gem if the gem installation directory is writable
		if writable {
			cmd := exec.Command(bundle, "add", "dockerfile-rails",
				"--optimistic", "--group", "development", "--skip-install")
			cmd.Stdin = nil
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			pendingError = cmd.Run()
			if pendingError != nil {
				pendingError = errors.Wrap(pendingError, "Failed to add dockerfile-rails gem")
			} else {
				cmd = exec.Command(bundle, "install", "--quiet")
				cmd.Stdin = nil
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				pendingError = cmd.Run()
				if pendingError != nil {
					pendingError = errors.Wrap(pendingError, "Failed to install dockerfile-rails gem")
				} else {
					generatorInstalled = true
				}
			}
		}
	} else {
		// proceed using the already installed gem
		generatorInstalled = true
	}

	// ensure Gemfile.lock includes the x86_64-linux platform
	if out, err := exec.Command(bundle, "platform").Output(); err == nil {
		if !strings.Contains(string(out), "x86_64-linux") {
			cmd := exec.Command(bundle, "lock", "--add-platform", "x86_64-linux")
			if err := cmd.Run(); err != nil {
				return errors.Wrap(err, "Failed to add x86_64-linux platform, exiting")
			}
		}
	}

	// ensure fly.toml exists.  If present, the rails dockerfile generator will
	// add volumes, processes, release command and potentailly other configuration.
	flyToml := "fly.toml"
	_, err = os.Stat(flyToml)
	if os.IsNotExist(err) {
		// "touch" fly.toml
		file, err := os.Create(flyToml)
		if err != nil {
			return errors.Wrap(err, "Failed to create fly.toml")
		}
		file.Close()

		// inform caller of the presence of this file
		srcInfo.MergeConfig = &MergeConfigStruct{
			Name:      flyToml,
			Temporary: true,
		}
	}

	// base generate command
	args := []string{binrails, "generate", "dockerfile",
		"--label=fly_launch_runtime:rails"}

	// skip prompt to replace files if Dockerfile already exists
	_, err = os.Stat("Dockerfile")
	if !errors.Is(err, fs.ErrNotExist) {
		args = append(args, "--skip")

		if !generatorInstalled {
			return errors.Wrap(pendingError, "No Dockerfile found")
		}
	}

	// add postgres
	if postgres := plan.Postgres.Provider(); postgres != nil {
		args = append(args, "--postgresql", "--no-prepare")
	}

	// add redis
	if redis := plan.Redis.Provider(); redis != nil {
		args = append(args, "--redis")
	}

	// add additional flags from launch command
	if len(flags) > 0 {
		args = append(args, flags...)
	}

	// run command if the generator is available
	if generatorInstalled {
		fmt.Printf("Running: %s\n", strings.Join(args, " "))
		cmd := exec.Command(ruby, args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		pendingError = cmd.Run()

		if exitError, ok := pendingError.(*exec.ExitError); ok {
			if exitError.ExitCode() == 42 {
				// generator exited with code 42, which means existing
				// Dockerfile contains errors which will prevent deployment.
				pendingError = nil
				srcInfo.SkipDeploy = true
				srcInfo.DeployDocs = `
Correct the errors in your Dockerfile and run 'fly deploy' to
deploy your Rails app.

The following comand can be used to update your Dockerfile:

    ` + binrails + ` generate dockerfile
`
				fmt.Println()
			}
		}
	}

	// read dockerfile
	dockerfile, err := os.ReadFile("Dockerfile")
	if err == nil {
		if pendingError != nil {
			// generator may have failed, but Dockerfile was created - warn user
			fmt.Println("Error running dockerfile generator:", pendingError)
		}
	} else if pendingError != nil {
		// generator failed and Dockerfile was not created - return original error
		return errors.Wrap(pendingError, "Failed to generate Dockerfile")
	} else {
		// generator succeeded, but Dockerfile was not created - return error
		return errors.Wrap(err, "Failed to read Dockerfile")
	}

	// extract volume - handle both plain string and JSON format, but only allow one path
	re := regexp.MustCompile(`(?m)^VOLUME\s+(\[\s*")?(\/[\w\/]*?(\w+))("\s*\])?\s*$`)
	m := re.FindStringSubmatch(string(dockerfile))

	if len(m) > 0 {
		srcInfo.Volumes = []Volume{
			{
				Source:      m[3], // last part of path
				Destination: m[2], // full path
			},
		}
	}

	// add HealthCheck (if found)
	srcInfo.HttpCheckPath = <-healthcheck_channel
	if srcInfo.HttpCheckPath != "" {
		srcInfo.HttpCheckHeaders = map[string]string{"X-Forwarded-Proto": "https"}
	}

	return nil
}

func RailsFailureCallback(err error) error {
	suggestion := flyerr.GetErrorSuggestion(err)

	if suggestion == "" {
		err = flyerr.GenericErr{
			Err: err.Error(),
			Suggest: "\nSee https://fly.io/docs/rails/getting-started/existing/#common-initial-deployment-issues\n" +
				"for suggestions on how to resolve common deployment issues.",
		}
	}

	return err
}
