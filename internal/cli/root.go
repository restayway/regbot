// Package cli implements the Regbot command-line interface.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/restayway/regbot/internal/config"
	"github.com/restayway/regbot/internal/engine"
	"github.com/restayway/regbot/internal/factory"
	"github.com/restayway/regbot/internal/hook"
	"github.com/restayway/regbot/internal/report"
	"github.com/restayway/regbot/internal/server"
	"github.com/restayway/regbot/pkg/plan"
	"github.com/spf13/cobra"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

type options struct {
	configPath string
	output     string
	outPath    string
	logFormat  string
	logLevel   string
}

func Execute(ctx context.Context, stdout, stderr io.Writer, args []string) int {
	root := New(stdout, stderr)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		var coded exitError
		if errors.As(err, &coded) {
			return coded.code
		}
		switch {
		case errors.Is(err, engine.ErrSafety):
			return 4
		case errors.Is(err, engine.ErrStalePlan):
			return 5
		case errors.Is(err, engine.ErrPartialApply):
			return 6
		default:
			return 1
		}
	}
	return 0
}

func New(stdout, stderr io.Writer) *cobra.Command {
	opts := &options{}
	root := &cobra.Command{
		Use: "regbot", Short: "Safe retention policies for OCI registries and GHCR",
		SilenceUsage: true, SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().StringVarP(&opts.configPath, "config", "c", "regbot.yaml", "configuration file")
	root.PersistentFlags().StringVar(&opts.logFormat, "log-format", "text", "log format: text or json")
	root.PersistentFlags().StringVar(&opts.logLevel, "log-level", "info", "log level")
	root.AddCommand(
		validateCommand(opts, stdout, stderr), planCommand(opts, stdout, stderr),
		applyCommand(opts, stdout, stderr), runCommand(opts, stdout, stderr),
		serveCommand(opts, stdout, stderr), healthcheckCommand(stdout),
		versionCommand(stdout),
	)
	return root
}

func serveCommand(opts *options, stdout, stderr io.Writer) *cobra.Command {
	var address, tokenEnv, tokenFile string
	command := &cobra.Command{
		Use: "serve", Short: "Expose health, metrics, and an HTTP run endpoint",
		RunE: func(cmd *cobra.Command, _ []string) error {
			runner, err := load(opts, stderr)
			if err != nil {
				return configError(err)
			}
			if err := runner.Validate(cmd.Context()); err != nil {
				return err
			}
			token, err := config.Secret(tokenEnv, tokenFile)
			if err != nil {
				return configError(err)
			}
			fmt.Fprintf(stdout, "Listening on %s\n", address)
			return (&server.Server{Address: address, Token: token, Engine: runner, Logger: runner.Logger}).ListenAndServe(cmd.Context())
		},
	}
	command.Flags().StringVar(&address, "listen", "127.0.0.1:8080", "HTTP listen address")
	command.Flags().StringVar(&tokenEnv, "run-token-env", "", "environment variable containing the /run bearer token")
	command.Flags().StringVar(&tokenFile, "run-token-file", "", "file containing the /run bearer token")
	command.MarkFlagsMutuallyExclusive("run-token-env", "run-token-file")
	return command
}

func healthcheckCommand(stdout io.Writer) *cobra.Command {
	var endpoint string
	var timeout time.Duration
	command := &cobra.Command{
		Use: "healthcheck", Short: "Check a running Regbot HTTP endpoint",
		RunE: func(cmd *cobra.Command, _ []string) error {
			parsed, err := url.Parse(endpoint)
			if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
				return configError(fmt.Errorf("invalid healthcheck URL %q", endpoint))
			}
			client := &http.Client{Timeout: timeout}
			request, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, parsed.String(), nil)
			if err != nil {
				return err
			}
			response, err := client.Do(request)
			if err != nil {
				return fmt.Errorf("healthcheck failed: %w", err)
			}
			defer response.Body.Close()
			if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
				return fmt.Errorf("healthcheck returned %s", response.Status)
			}
			fmt.Fprintln(stdout, "healthy")
			return nil
		},
	}
	command.Flags().StringVar(&endpoint, "url", "http://127.0.0.1:8080/healthz", "health endpoint URL")
	command.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "request timeout")
	return command
}

func validateCommand(opts *options, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use: "validate", Short: "Validate configuration and provider connectivity",
		RunE: func(cmd *cobra.Command, _ []string) error {
			runner, err := load(opts, stderr)
			if err != nil {
				return configError(err)
			}
			if err := runner.Validate(cmd.Context()); err != nil {
				return fmt.Errorf("provider validation failed: %w", err)
			}
			fmt.Fprintln(stdout, "Configuration and provider connectivity are valid.")
			return nil
		},
	}
}

func planCommand(opts *options, stdout, stderr io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use: "plan", Short: "Discover artifacts and create a deletion plan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			runner, err := load(opts, stderr)
			if err != nil {
				return configError(err)
			}
			proposal, err := runner.Plan(cmd.Context())
			if err != nil {
				return err
			}
			if opts.outPath != "" {
				if err := writeJSONAtomic(opts.outPath, proposal); err != nil {
					return err
				}
			}
			return renderPlan(stdout, opts.output, proposal)
		},
	}
	command.Flags().StringVar(&opts.output, "output", "table", "output format: table or json")
	command.Flags().StringVar(&opts.outPath, "out", "", "write the JSON plan to a file")
	return command
}

func applyCommand(opts *options, stdout, stderr io.Writer) *cobra.Command {
	var planPath string
	command := &cobra.Command{
		Use: "apply", Short: "Apply an immutable deletion plan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if planPath == "" {
				return configError(errors.New("--plan is required"))
			}
			runner, err := load(opts, stderr)
			if err != nil {
				return configError(err)
			}
			proposal, err := readPlan(planPath)
			if err != nil {
				return configError(err)
			}
			result, applyErr := runner.Apply(cmd.Context(), proposal)
			if err := renderResult(stdout, opts.output, result); err != nil {
				return err
			}
			deliverHookLogged(cmd.Context(), runner, result)
			return applyErr
		},
	}
	command.Flags().StringVar(&planPath, "plan", "", "JSON plan file")
	command.Flags().StringVar(&opts.output, "output", "table", "output format: table or json")
	return command
}

func runCommand(opts *options, stdout, stderr io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use: "run", Short: "Plan and optionally apply in one invocation",
		RunE: func(cmd *cobra.Command, _ []string) error {
			runner, err := load(opts, stderr)
			if err != nil {
				return configError(err)
			}
			proposal, err := runner.Plan(cmd.Context())
			if err != nil {
				return err
			}
			if !runner.Config.Apply {
				return renderPlan(stdout, opts.output, proposal)
			}
			result, applyErr := runner.Apply(cmd.Context(), proposal)
			if err := renderResult(stdout, opts.output, result); err != nil {
				return err
			}
			deliverHookLogged(cmd.Context(), runner, result)
			return applyErr
		},
	}
	command.Flags().StringVar(&opts.output, "output", "table", "output format: table or json")
	return command
}

func versionCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "Print build information",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Fprintf(stdout, "regbot %s (commit=%s, built=%s)\n", Version, Commit, Date)
		},
	}
}

func load(opts *options, stderr io.Writer) (*engine.Engine, error) {
	cfg, data, err := config.Load(opts.configPath)
	if err != nil {
		return nil, err
	}
	providers, err := factory.Providers(cfg)
	if err != nil {
		return nil, err
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(opts.logLevel)); err != nil {
		return nil, err
	}
	handlerOptions := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch opts.logFormat {
	case "text":
		handler = slog.NewTextHandler(stderr, handlerOptions)
	case "json":
		handler = slog.NewJSONHandler(stderr, handlerOptions)
	default:
		return nil, fmt.Errorf("unsupported log format %q", opts.logFormat)
	}
	return &engine.Engine{Config: cfg, ConfigBytes: data, Providers: providers, Logger: slog.New(handler)}, nil
}

func readPlan(path string) (plan.Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return plan.Plan{}, err
	}
	var proposal plan.Plan
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proposal); err != nil {
		return plan.Plan{}, fmt.Errorf("decode plan: %w", err)
	}
	return proposal, nil
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".regbot-plan-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func renderPlan(writer io.Writer, format string, proposal plan.Plan) error {
	switch format {
	case "table":
		return report.PlanTable(writer, proposal)
	case "json":
		return report.JSON(writer, proposal)
	default:
		return configError(fmt.Errorf("unsupported output format %q", format))
	}
}

func renderResult(writer io.Writer, format string, result plan.Result) error {
	switch format {
	case "table":
		return report.ResultTable(writer, result)
	case "json":
		return report.JSON(writer, result)
	default:
		return configError(fmt.Errorf("unsupported output format %q", format))
	}
}

func deliverHookLogged(ctx context.Context, runner *engine.Engine, result plan.Result) {
	if result.Deleted == 0 {
		return
	}
	if err := deliverHook(ctx, runner.Config, result); err != nil {
		runner.Logger.Error("post-apply webhook failed", "error", err)
	}
}

func deliverHook(ctx context.Context, cfg *config.Config, result plan.Result) error {
	configured := cfg.Hooks.AfterApply
	if configured == nil {
		return nil
	}
	endpoint, err := config.Secret(configured.URLEnv, "")
	if err != nil {
		return err
	}
	token, err := config.Secret(configured.BearerTokenEnv, "")
	if err != nil {
		return err
	}
	secret, err := config.Secret(configured.HMACSecretEnv, "")
	if err != nil {
		return err
	}
	return (hook.Webhook{URL: endpoint, BearerToken: token, HMACSecret: secret, Timeout: configured.Timeout.Duration}).Deliver(ctx, result)
}

type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }
func configError(err error) error { return exitError{code: 2, err: err} }
