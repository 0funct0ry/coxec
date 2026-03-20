package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"math/rand/v2"
	"strings"
	"syscall"
	"time"

	"github.com/0funct0ry/coxec/internal/engine"
	"github.com/spf13/cobra"
)

// Version string populated at build time
var Version = "dev"

const validationExitCode = 3

func validateExecutionSource(executeCmd, fileFlag, templateFlag string) error {
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

var rootCmd = &cobra.Command{
	Use:   "coxec",
	Short: "A swiss army knife for concurrent execution",
	Long: `coxec is a CLI tool and server for concurrent execution, providing templates, built-in clients, timing control, and structured output.

Execution source (exactly one required):
  -e, --exec string      Shell command or built-in to execute repeatedly
  -f, --file string      Path to shell script file to execute repeatedly
  -t, --template string  Path to Go template file defining the execution plan

Built-in clients execute natively without spawning a shell:
  .http                  Execute HTTP requests natively
  .tcp                   Execute TCP connections natively
  .sleep                 Pause execution for a duration
  (Names starting with '.' are recognized as built-ins; others fall back to shell)

By default: only command stdout appears on stdout; summary and diagnostics go to stderr.
Use -v / --verbose to see detailed per-execution information on stderr.
Use --silent to suppress all output from executed commands.
Use 2>/dev/null or redirect stderr to hide the summary.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		versionFlag, _ := cmd.Flags().GetBool("version")
		verboseFlag, _ := cmd.Flags().GetBool("verbose")
		silentFlag, _ := cmd.Flags().GetBool("silent")
		reportFlag, _ := cmd.Flags().GetBool("report")
		if versionFlag {
			fmt.Printf("coxec version %s\n", Version)
			return nil
		}

		executeCmd, _ := cmd.Flags().GetString("execute")
		fileFlag, _ := cmd.Flags().GetString("file")
		templateFlag, _ := cmd.Flags().GetString("template")
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		iterations, _ := cmd.Flags().GetInt("iterations")
		timeout, _ := cmd.Flags().GetDuration("timeout")
		globalTimeout, _ := cmd.Flags().GetDuration("global-timeout")
		delay, _ := cmd.Flags().GetDuration("delay")
		jitter, _ := cmd.Flags().GetDuration("jitter")
		userVarsRaw, _ := cmd.Flags().GetStringArray("var")
		userVars, err := parseUserVars(userVarsRaw)
		if err != nil {
			return err
		}

		if !cmd.Flags().Changed("iterations") {
			iterations = concurrency
		}

		if err := validateExecutionSource(executeCmd, fileFlag, templateFlag); err != nil {
			return err
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

		registry := getBuiltinRegistry()

		templateState := engine.NewTemplateState()

		// Set up global context with interrupt signal handling
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		// If global timeout is set, wrap the context
		if globalTimeout > 0 {
			var globalCancel context.CancelFunc
			ctx, globalCancel = context.WithTimeout(ctx, globalTimeout)
			defer globalCancel()
		}

		if executeCmd != "" {
			// Disable printing usage to avoid cluttering stderr on command failure
			cmd.SilenceUsage = true
			// We handle the error exiting locally to propagate exit code
			cmd.SilenceErrors = true

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
			}

			go func() {
				defer close(tasks)
				for i := 0; i < iterations; i++ {
					if i > 0 && (delay > 0 || jitter > 0) {
						appliedDelay := delay
						if jitter > 0 {
							// Uniformly between [delay - jitter, delay + jitter]
							jf := float64(jitter)
							randomJitter := time.Duration(jf * (2*rand.Float64() - 1))
							appliedDelay += randomJitter
							if appliedDelay < 0 {
								appliedDelay = 0
							}
						}

						if appliedDelay > 0 {
							select {
							case <-ctx.Done():
								return
							case <-time.After(appliedDelay):
							}
						}
					}
					select {
					case <-ctx.Done():
						return
					case tasks <- engine.Task{Index: i + 1, Command: executeCmd, Timestamp: time.Now()}:
					}
				}
			}()

			return engine.RunJobPool(actualConcurrency, tasks, opts)
		}

		if fileFlag != "" {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true

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
			}

			go func() {
				defer close(tasks)
				for i := 0; i < iterations; i++ {
					if i > 0 && (delay > 0 || jitter > 0) {
						appliedDelay := delay
						if jitter > 0 {
							jf := float64(jitter)
							randomJitter := time.Duration(jf * (2*rand.Float64() - 1))
							appliedDelay += randomJitter
							if appliedDelay < 0 {
								appliedDelay = 0
							}
						}

						if appliedDelay > 0 {
							select {
							case <-ctx.Done():
								return
							case <-time.After(appliedDelay):
							}
						}
					}
					select {
					case <-ctx.Done():
						return
					case tasks <- engine.Task{Index: i + 1, Command: string(scriptContent), Timestamp: time.Now()}:
					}
				}
			}()

			return engine.RunJobPool(actualConcurrency, tasks, opts)
		}

		if templateFlag != "" {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true

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
			}

			go func() {
				defer close(tasks)
				for i := 0; i < iterations; i++ {
					if i > 0 && (delay > 0 || jitter > 0) {
						appliedDelay := delay
						if jitter > 0 {
							jf := float64(jitter)
							randomJitter := time.Duration(jf * (2*rand.Float64() - 1))
							appliedDelay += randomJitter
							if appliedDelay < 0 {
								appliedDelay = 0
							}
						}

						if appliedDelay > 0 {
							select {
							case <-ctx.Done():
								return
							case <-time.After(appliedDelay):
							}
						}
					}
					select {
					case <-ctx.Done():
						return
					case tasks <- engine.Task{Index: i + 1, Command: string(tplContent), Timestamp: time.Now()}:
					}
				}
			}()

			return engine.RunJobPool(actualConcurrency, tasks, opts)
		}

		return nil
	},
}

func init() {
	rootCmd.Flags().Bool("version", false, "Print the version number")
	rootCmd.Flags().BoolP("verbose", "v", false, "Show detailed per-execution information on stderr")
	rootCmd.Flags().Bool("silent", false, "Suppress child stdout/stderr payload")
	rootCmd.Flags().Bool("report", false, "Include HTTP-specific error breakdown in the execution output")
	rootCmd.Flags().StringP("execute", "e", "", "Shell command or built-in to execute repeatedly")
	rootCmd.Flags().StringP("file", "f", "", "Path to shell script file to execute repeatedly")
	rootCmd.Flags().StringP("template", "t", "", "Path to Go template file defining the execution plan")
	rootCmd.Flags().IntP("concurrency", "c", 1, "Number of concurrent executions")
	rootCmd.Flags().IntP("iterations", "n", -1, "Total number of executions (defaults to concurrency)")
	rootCmd.Flags().Duration("timeout", 0, "Maximum allowed duration for each individual execution (e.g. 5s, 100ms)")
	rootCmd.Flags().Duration("global-timeout", 0, "Maximum total wall-clock time for the entire run (e.g. 15m, 1h)")
	rootCmd.Flags().Duration("delay", 0, "Fixed delay between worker starts (e.g. 400ms, 1s)")
	rootCmd.Flags().Duration("jitter", 0, "Random jitter added to delay (e.g. 100ms). Final delay is delay ± jitter")
	rootCmd.Flags().StringArray("var", nil, "Set user variables (key=value)")
	rootCmd.Flags().Bool("json", false, "Output validation errors as JSON")

	// Register built-in client subcommands for help and discovery
	registry := getBuiltinRegistry()
	builtinGroup := &cobra.Group{ID: "builtins", Title: "Available Built-in clients:"}
	rootCmd.AddGroup(builtinGroup)

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
		rootCmd.AddCommand(builtinCmd)
	}
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
