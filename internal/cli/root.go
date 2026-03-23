package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/0funct0ry/coxec/internal/config"
	"github.com/0funct0ry/coxec/internal/engine"
	"github.com/0funct0ry/coxec/internal/server"
	"github.com/spf13/cobra"
)

// Version string populated at build time
var Version = "dev"

const validationExitCode = 3

func validateExecutionSource(executeCmd, fileFlag, templateFlag string, serverFlag bool) error {
	if serverFlag {
		return nil
	}
	hasExec := executeCmd != ""
	hasFile := fileFlag != ""
	hasTemplate := templateFlag != ""
	count := 0
	if hasExec {
		count++
	}
	if hasFile {
		count++
	}
	if hasTemplate {
		count++
	}

	if count == 0 {
		return &ValidationError{
			ExitCode:   validationExitCode,
			ID:         "INVALID_ARGS",
			Message:    "No execution source specified. Use one of -e, -f, or -t",
			Suggestion: "use -e for inline commands, -f for shell scripts, or -t for template files",
		}
	}
	if count >= 2 {
		msg := ""
		suggestion := ""
		switch {
		case hasExec && hasFile:
			msg = "flags -e and -f are mutually exclusive"
			suggestion = "use -e for inline commands or -f for shell scripts, not both"
		case hasExec && hasTemplate:
			msg = "flags -e and -t are mutually exclusive"
			suggestion = "use -e for inline commands or -t for template files, not both"
		case hasFile && hasTemplate:
			msg = "flags -f and -t are mutually exclusive"
			suggestion = "use -f for shell scripts or -t for template files, not both"
		default:
			msg = "flags -e, -f, and -t are mutually exclusive"
			suggestion = "use exactly one execution source flag"
		}
		return &ValidationError{ExitCode: validationExitCode, ID: "INVALID_ARGS", Message: msg, Suggestion: suggestion}
	}
	return nil
}

func NewRootCmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "coxec",
		Short: "A swiss army knife for concurrent execution",
		Long: `coxec is a CLI tool and server for concurrent execution, providing templates, built-in clients, timing control, and structured output.

Execution source (exactly one required):
  -e, --exec string      Shell command or built-in to execute repeatedly
  -f, --file string      Path to shell script file to execute repeatedly
  -t, --template string  Path to Go template file defining the execution plan
  -s, --server           Start as an HTTP server to execute commands remotely

Built-in clients execute natively without spawning a shell:
  .http                  Execute HTTP requests natively
  .tcp                   Execute TCP connections natively
  .sleep                 Pause execution for a duration
  (Names starting with '.' are recognized as built-ins; others fall back to shell)

Server-only flags (when using -s):
  -a, --addr string      Bind address (default: 127.0.0.1)
  -p, --port int         Listening port (default: 8080)
  --tls-cert string      Path to TLS certificate file (PEM format)
  --tls-key string       Path to TLS private key file (PEM format)

By default: only command stdout appears on stdout; summary and diagnostics go to stderr.
Use -v / --verbose to see detailed per-execution information on stderr.
Use --silent to suppress all output from executed commands.
Use 2>/dev/null or redirect stderr to hide the summary.
`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Version flag handled by cobra
		versionFlag, _ := cmd.Flags().GetBool("version")
		if versionFlag {
			fmt.Printf("coxec version %s\n", Version)
			return nil
		}

		verboseFlag, _ := cmd.Flags().GetBool("verbose")
		silentFlag, _ := cmd.Flags().GetBool("silent")
		reportFlag, _ := cmd.Flags().GetBool("report")
		serverFlag, _ := cmd.Flags().GetBool("server")
		configPath, _ := cmd.Flags().GetString("config")

		v := config.InitViper()
		// Bind server flags to viper
		v.BindPFlag("server.addr", cmd.Flags().Lookup("addr"))
		v.BindPFlag("server.port", cmd.Flags().Lookup("port"))
		v.BindPFlag("server.auth-token", cmd.Flags().Lookup("auth-token"))
		v.BindPFlag("server.auth-basic", cmd.Flags().Lookup("auth-basic"))
		v.BindPFlag("server.auth-hmac-secret", cmd.Flags().Lookup("auth-hmac-secret"))
		v.BindPFlag("server.tls.cert", cmd.Flags().Lookup("tls-cert"))
		v.BindPFlag("server.tls.key", cmd.Flags().Lookup("tls-key"))
		v.BindPFlag("server.default-concurrency", cmd.Flags().Lookup("concurrency"))
		v.BindPFlag("server.default-iterations", cmd.Flags().Lookup("iterations"))
		v.BindPFlag("server.max-concurrent-jobs", cmd.Flags().Lookup("max-concurrent-jobs"))
		v.BindPFlag("server.enable-sync", cmd.Flags().Lookup("enable-sync"))

		loadedConfig, err := config.LoadConfig(v, configPath)
		if err != nil {
			return err
		}

		addr := v.GetString("server.addr")
		port := v.GetInt("server.port")
		authToken := v.GetString("server.auth-token")
		authBasic := v.GetString("server.auth-basic")
		authHmacSecret := v.GetString("server.auth-hmac-secret")
		tlsCert := v.GetString("server.tls.cert")
		tlsKey := v.GetString("server.tls.key")
		concurrency := v.GetInt("server.default-concurrency")
		iterations := v.GetInt("server.default-iterations")
		maxConcurrentJobs := v.GetInt("server.max-concurrent-jobs")
		enableSync := v.GetBool("server.enable-sync")

		// These flags are still needed for CLI mode or if they are not bound to viper yet
		executeCmd, _ := cmd.Flags().GetString("execute")
		fileFlag, _ := cmd.Flags().GetString("file")
		templateFlag, _ := cmd.Flags().GetString("template")
		// iterations/concurrency already handled by viper if in server mode, 
		// but let's keep the CLI logic as is for non-server mode.
		if !serverFlag {
			concurrency, _ = cmd.Flags().GetInt("concurrency")
			iterations, _ = cmd.Flags().GetInt("iterations")
			if !cmd.Flags().Changed("iterations") {
				iterations = concurrency
			}
		}

		timeout, _ := cmd.Flags().GetDuration("timeout")
		globalTimeout, _ := cmd.Flags().GetDuration("global-timeout")
		delay, _ := cmd.Flags().GetDuration("delay")
		jitter, _ := cmd.Flags().GetDuration("jitter")
		rateFlag, _ := cmd.Flags().GetString("rate")
		rateLimit, err := parseRate(rateFlag)
		if err != nil {
			return err
		}
		userVarsRaw, _ := cmd.Flags().GetStringArray("var")
		userVars, err := parseUserVars(userVarsRaw)
		if err != nil {
			return err
		}

		rampup, _ := cmd.Flags().GetDuration("rampup")

		if err := validateExecutionSource(executeCmd, fileFlag, templateFlag, serverFlag); err != nil {
			return err
		}

		// Validate TLS flags
		if (tlsCert != "") != (tlsKey != "") {
			return &ValidationError{
				ExitCode:   validationExitCode,
				ID:         "INVALID_AUTH_FLAGS",
				Message:    "both --tls-cert and --tls-key must be provided for HTTPS",
				Suggestion: "provide both flags, or none to use HTTP",
			}
		}

		registry := getBuiltinRegistry()

		templateState := engine.NewTemplateState()

		// Set up global context with interrupt signal handling
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		// If global timeout is set, wrap the context
		if globalTimeout > 0 {
			var globalCancel context.CancelFunc
			ctx, globalCancel = context.WithTimeout(ctx, globalTimeout)
			defer globalCancel()
		}

		if serverFlag {
			authFlagsSet := 0
			if authToken != "" {
				authFlagsSet++
			}
			if authBasic != "" {
				authFlagsSet++
			}
			if authHmacSecret != "" {
				authFlagsSet++
			}

			if authFlagsSet > 1 {
				return &ValidationError{
					ExitCode:   validationExitCode,
					ID:         "INVALID_AUTH_FLAGS",
					Message:    "authentication flags (--auth-token, --auth-basic, --auth-hmac-secret) are mutually exclusive",
					Suggestion: "choose only one authentication method",
				}
			}

			if authFlagsSet == 0 {
				fmt.Fprintln(os.Stderr, "WARNING: Starting server without authentication. This is insecure and should only be used for local testing.")
			}
			if loadedConfig != "" {
				fmt.Fprintf(os.Stderr, "Config loaded from: %s\n", loadedConfig)
			}
			s := server.NewServer(addr, port, Version, authToken, authBasic, authHmacSecret, tlsCert, tlsKey, registry, concurrency, iterations, maxConcurrentJobs, enableSync, server.NewInMemoryJobStore())
			return s.Start(ctx)
		}

		if concurrency <= 0 {
			return fmt.Errorf("concurrency (-c) must be greater than 0")
		}

		if iterations == 0 {
			fmt.Fprintln(os.Stderr, "No executions requested (-n 0)")
			return nil
		}

		if iterations < 0 {
			return fmt.Errorf("iterations (-n) must be greater than or equal to 0")
		}

		var activeCount atomic.Int32

		if executeCmd != "" {
			actualConcurrency := concurrency
			if iterations < concurrency {
				actualConcurrency = iterations
			}

			tasks := make(chan engine.Task, actualConcurrency)
			opts := engine.ExecOptions{
				Verbose:       verboseFlag,
				Silent:        silentFlag,
				Report:        reportFlag,
				TotalTasks:    iterations,
				Context:       ctx,
				Stdout:        os.Stdout,
				Stderr:        os.Stderr,
				UserVars:      userVars,
				Registry:      registry,
				TemplateState: templateState,
				Timeout:       timeout,
				Delay:         delay,
				Jitter:        jitter,
				RampUp:        rampup,
				ActiveCount:   &activeCount,
				RateLimit:     rateLimit,
			}

			startTaskGenerator(ctx, tasks, iterations, executeCmd, delay, jitter, rateLimit, verboseFlag)

			_, err = engine.RunJobPool(actualConcurrency, tasks, opts)
			return err
		}

		if fileFlag != "" {
			scriptContent, err := os.ReadFile(fileFlag)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return &ValidationError{
						ExitCode:   validationExitCode,
						ID:         "FILE_NOT_FOUND",
						Message:    fmt.Sprintf("script file not found: %s", fileFlag),
						Suggestion: "check the file path; it is resolved relative to the current working directory",
					}
				}
				if errors.Is(err, os.ErrPermission) {
					return &ValidationError{
						ExitCode:   validationExitCode,
						ID:         "FILE_READ_ERROR",
						Message:    fmt.Sprintf("cannot read script file %s: permission denied", fileFlag),
						Suggestion: "check file permissions",
					}
				}
				return fmt.Errorf("Error: failed to read script file %s: %w", fileFlag, err)
			}

			actualConcurrency := concurrency
			if iterations < concurrency {
				actualConcurrency = iterations
			}

			tasks := make(chan engine.Task, actualConcurrency)
			opts := engine.ExecOptions{
				Verbose:       verboseFlag,
				Silent:        silentFlag,
				Report:        reportFlag,
				TotalTasks:    iterations,
				Context:       ctx,
				Stdout:        os.Stdout,
				Stderr:        os.Stderr,
				UserVars:      userVars,
				Registry:      registry,
				TemplateState: templateState,
				Timeout:       timeout,
				Delay:         delay,
				Jitter:        jitter,
				RampUp:        rampup,
				ActiveCount:   &activeCount,
				RateLimit:     rateLimit,
			}

			startTaskGenerator(ctx, tasks, iterations, string(scriptContent), delay, jitter, rateLimit, verboseFlag)

			_, err = engine.RunJobPool(actualConcurrency, tasks, opts)
			return err
		}

		if templateFlag != "" {
			tplContent, err := os.ReadFile(templateFlag)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return &ValidationError{
						ExitCode:   validationExitCode,
						ID:         "FILE_NOT_FOUND",
						Message:    fmt.Sprintf("template file not found: %s", templateFlag),
						Suggestion: "check the file path; it is resolved relative to the current working directory",
					}
				}
				if errors.Is(err, os.ErrPermission) {
					return &ValidationError{
						ExitCode:   validationExitCode,
						ID:         "FILE_READ_ERROR",
						Message:    fmt.Sprintf("cannot read template file %s: permission denied", templateFlag),
						Suggestion: "check file permissions",
					}
				}
				return fmt.Errorf("Error: failed to read template file %s: %w", templateFlag, err)
			}

			tplStr := string(tplContent)
			if strings.TrimSpace(tplStr) == "" {
				return &ValidationError{
					ExitCode:   validationExitCode,
					ID:         "EMPTY_TEMPLATE",
					Message:    fmt.Sprintf("template file rendered to empty string: %s", templateFlag),
					Suggestion: "the file may contain only template comments or whitespace-trimming markers",
				}
			}

			// The template itself defines the execution plan, so we don't just pass tplContent as a command.
			// Instead, we will pass the template string to the engine to parse and execute.
			// The engine will then generate the actual commands/pipelines.
			// For now, we'll just validate the template. The actual execution logic will be more complex.
			if err := engine.ValidateTemplate(templateFlag, tplStr, templateState); err != nil {
				if te, ok := err.(*engine.TemplateError); ok {
					return &ValidationError{
						ExitCode:   validationExitCode,
						ID:         "INVALID_TEMPLATE",
						Message:    fmt.Sprintf("template parse error: %s", te.Error()),
						Suggestion: te.Suggestion,
					}
				}
				return &ValidationError{
					ExitCode:   validationExitCode,
					ID:         "INVALID_TEMPLATE",
					Message:    fmt.Sprintf("template parse error: %v", err),
					Suggestion: "check template syntax",
				}
			}

			actualConcurrency := concurrency
			if iterations < concurrency {
				actualConcurrency = iterations
			}

			tasks := make(chan engine.Task, actualConcurrency)
			opts := engine.ExecOptions{
				Verbose:       verboseFlag,
				Silent:        silentFlag,
				Report:        reportFlag,
				TotalTasks:    iterations,
				Context:       ctx,
				Stdout:        os.Stdout,
				Stderr:        os.Stderr,
				UserVars:      userVars,
				Registry:      registry,
				TemplateState: templateState,
				Timeout:       timeout,
				Delay:         delay,
				Jitter:        jitter,
				RampUp:        rampup,
				ActiveCount:   &activeCount,
				RateLimit:     rateLimit,
			}

			startTaskGenerator(ctx, tasks, iterations, string(tplContent), delay, jitter, rateLimit, verboseFlag)

			_, err = engine.RunJobPool(actualConcurrency, tasks, opts)
			return err
		}

		return nil
	},
	}

	cmd.Flags().Bool("version", false, "Print the version number")
	cmd.Flags().BoolP("verbose", "v", false, "Show detailed per-execution information on stderr")
	cmd.Flags().Bool("silent", false, "Suppress child stdout/stderr payload")
	cmd.Flags().Bool("report", false, "Include HTTP-specific error breakdown in the execution output")
	cmd.Flags().StringP("execute", "e", "", "Shell command or built-in to execute repeatedly")
	cmd.Flags().StringP("file", "f", "", "Path to shell script file to execute repeatedly")
	cmd.Flags().StringP("template", "t", "", "Path to Go template file defining the execution plan")
	cmd.Flags().IntP("concurrency", "c", 1, "Number of concurrent executions")
	cmd.Flags().IntP("iterations", "n", -1, "Total number of executions (defaults to concurrency)")
	cmd.Flags().Duration("timeout", 0, "Maximum allowed duration for each individual execution (e.g. 5s, 100ms)")
	cmd.Flags().Duration("global-timeout", 0, "Maximum total wall-clock time for the entire run (e.g. 15m, 1h)")
	cmd.Flags().Duration("delay", 0, "Fixed delay between worker starts (e.g. 400ms, 1s)")
	cmd.Flags().Duration("jitter", 0, "Random jitter added to delay (e.g. 100ms). Final delay is delay ± jitter")
	cmd.Flags().Duration("rampup", 0, "Gradually increase concurrency over this duration (e.g. 30s, 2m)")
	cmd.Flags().String("rate", "", "Maximum rate of executions (e.g. 50/s, 100/m, 1/h)")
	cmd.Flags().StringArray("var", nil, "Set user variables (key=value)")
	cmd.Flags().Bool("json", false, "Output validation errors as JSON")
	cmd.Flags().BoolP("server", "s", false, "Start coxec in server mode")
	cmd.Flags().StringP("addr", "a", "127.0.0.1", "Bind address for the server")
	cmd.Flags().IntP("port", "p", 8080, "Port to listen on")
	cmd.Flags().String("auth-token", "", "Bearer token required for server API requests (except /health)")
	cmd.Flags().String("auth-basic", "", "Basic auth credentials in user:pass format required for server API requests (except /health)")
	cmd.Flags().String("auth-hmac-secret", "", "HMAC secret required for server API requests (except /health)")
	cmd.Flags().String("tls-cert", "", "Path to TLS certificate file (PEM format)")
	cmd.Flags().String("tls-key", "", "Path to TLS private key file (PEM format)")
	cmd.Flags().String("config", "", "Path to configuration file")
	cmd.Flags().Int("max-concurrent-jobs", 0, "Maximum number of concurrent jobs (requests) allowed globally")
	cmd.Flags().Bool("enable-sync", true, "Enable synchronous execution mode")

	// Register built-in client subcommands for help and discovery
	registry := getBuiltinRegistry()
	builtinGroup := &cobra.Group{ID: "builtins", Title: "Available Built-in clients:"}
	cmd.AddGroup(builtinGroup)

	for name, helpText := range registry.AllHelp() {
		builtinCmd := &cobra.Command{
			Use:     name,
			Short:   fmt.Sprintf("Help for %s built-in client", name),
			Long:    helpText,
			GroupID: builtinGroup.ID,
			Run: func(cmd *cobra.Command, args []string) {
				fmt.Printf("Built-in client: %s\n", cmd.Name())
				fmt.Println("To execute this built-in, use the -e, -f, or -t flags.")
				fmt.Printf("Example: coxec -e '%s GET https://api.example.com'\n", cmd.Name())
				fmt.Println("Note: Built-in names are prefixed with a dot to prevent shell conflicts.")
				fmt.Println()
				fmt.Print(cmd.Long)
				if !strings.HasSuffix(cmd.Long, "\n") {
					fmt.Println()
				}
			},
		}
		cmd.AddCommand(builtinCmd)
	}

	return cmd
}

var rootCmd = NewRootCmd()

func init() {
	// Root flags are now initialized in NewRootCmd
}

func getBuiltinRegistry() *engine.BuiltinRegistry {
	registry := engine.NewBuiltinRegistry()
	registry.Register(engine.NewHTTPClient())
	registry.Register(engine.NewTCPClient())
	registry.Register(engine.NewSleepClient())
	return registry
}

// Execute runs the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		jsonFlag, _ := rootCmd.Flags().GetBool("json")
		if ve, ok := err.(*ValidationError); ok {
			if jsonFlag {
				fmt.Fprintln(os.Stderr, ve.JSON())
			} else {
				fmt.Fprintln(os.Stderr, ve.String())
				fmt.Fprintln(os.Stderr)
				rootCmd.Usage()
			}
			os.Exit(ve.ExitCode)
		}
		if exitErr, ok := err.(*engine.ExitError); ok {
			os.Exit(exitErr.Code)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		// Fallback for other errors
		fmt.Fprintln(os.Stderr, err)
		os.Exit(10)
	}
}

func parseUserVars(vars []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, v := range vars {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			return nil, &ValidationError{
				ExitCode:   validationExitCode,
				ID:         "INVALID_ARGS",
				Message:    fmt.Sprintf("invalid variable format '%s'. Must be key=value", v),
				Suggestion: "ensure variables are provided as key=value",
			}
		}
		result[parts[0]] = parts[1]
	}
	return result, nil
}

func parseRate(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	parts := strings.Split(s, "/")
	val, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid rate value: %w", err)
	}
	if len(parts) == 1 {
		return val, nil // Default to per second
	}
	switch strings.ToLower(parts[1]) {
	case "s", "sec", "second", "seconds":
		return val, nil
	case "m", "min", "minute", "minutes":
		return val / 60.0, nil
	case "h", "hr", "hour", "hours":
		return val / 3600.0, nil
	default:
		return 0, fmt.Errorf("invalid rate unit: %s", parts[1])
	}
}

func startTaskGenerator(ctx context.Context, tasks chan<- engine.Task, iterations int, command string, delay, jitter time.Duration, rateLimit float64, verbose bool) {
	go func() {
		defer close(tasks)
		var lastStart time.Time

		rateInterval := time.Duration(0)
		if rateLimit > 0 {
			rateInterval = time.Duration(float64(time.Second) / rateLimit)
		}

		for i := 0; i < iterations; i++ {
			now := time.Now()
			var waitDuration time.Duration

			if i > 0 {
				// 1. Calculate rate-based wait
				if rateLimit > 0 {
					target := lastStart.Add(rateInterval)
					if target.After(now) {
						waitDuration = target.Sub(now)
					}
				}

				// 2. Calculate delay/jitter-based wait
				if delay > 0 || jitter > 0 {
					d := delay
					if jitter > 0 {
						jf := float64(jitter)
						randomJitter := time.Duration(jf * (2*rand.Float64() - 1))
						d += randomJitter
						if d < 0 {
							d = 0
						}
					}
					if d > waitDuration {
						waitDuration = d
					}
				}

				if waitDuration > 0 {
					if verbose {
						fmt.Fprintf(os.Stderr, "Rate limiting: waiting %v...\n", waitDuration.Round(time.Millisecond))
					}
					select {
					case <-ctx.Done():
						return
					case <-time.After(waitDuration):
						now = time.Now()
					}
				}
			}

			lastStart = now
			select {
			case <-ctx.Done():
				return
			case tasks <- engine.Task{Index: i + 1, Command: command, Timestamp: time.Now()}:
			}
		}
	}()
}
