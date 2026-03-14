package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/0funct0ry/coxec/internal/engine"
	"github.com/spf13/cobra"
)

// Version string populated at build time
var Version = "dev"

const validationExitCode = 64

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
			Code: validationExitCode,
			Msg:  "Error: No execution source specified. Use one of -e, -f, or -t",
		}
	}
	if count >= 2 {
		msg := "Error: "
		switch {
		case hasExec && hasFile:
			msg += "cannot use both -e / --exec and -f / --file at the same time"
		case hasExec && hasTemplate:
			msg += "cannot use both -e / --exec and -t / --template at the same time"
		case hasFile && hasTemplate:
			msg += "cannot use both -f / --file and -t / --template at the same time"
		default:
			msg += "cannot use -e, -f, and -t at the same time"
		}
		msg += "\nOnly one execution source is allowed."
		return &ValidationError{Code: validationExitCode, Msg: msg}
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

By default: only command stdout appears on stdout; summary and diagnostics go to stderr.
Use -v / --verbose to see detailed per-execution information on stderr.
Use --silent to suppress all output from executed commands.
Use 2>/dev/null or redirect stderr to hide the summary.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		versionFlag, _ := cmd.Flags().GetBool("version")
		verboseFlag, _ := cmd.Flags().GetBool("verbose")
		silentFlag, _ := cmd.Flags().GetBool("silent")
		if versionFlag {
			fmt.Printf("coxec version %s\n", Version)
			return nil
		}

		executeCmd, _ := cmd.Flags().GetString("execute")
		fileFlag, _ := cmd.Flags().GetString("file")
		templateFlag, _ := cmd.Flags().GetString("template")
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		iterations, _ := cmd.Flags().GetInt("iterations")

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

			go func() {
				for i := 0; i < iterations; i++ {
					tasks <- engine.Task{Index: i + 1, Command: executeCmd}
				}
				close(tasks)
			}()

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			opts := engine.ExecOptions{
				Verbose:    verboseFlag,
				Silent:     silentFlag,
				TotalTasks: iterations,
				Context:    ctx, Stdout: os.Stdout, Stderr: os.Stderr,
			}

			return engine.RunJobPool(actualConcurrency, tasks, opts)
		}

		if fileFlag != "" {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true

			scriptContent, err := os.ReadFile(fileFlag)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return &ValidationError{
						Code: validationExitCode,
						Msg:  fmt.Sprintf("Error: script file not found: %s", fileFlag),
					}
				}
				return fmt.Errorf("Error: failed to read script file %s: %w", fileFlag, err)
			}

			actualConcurrency := concurrency
			if iterations < concurrency {
				actualConcurrency = iterations
			}

			tasks := make(chan engine.Task, actualConcurrency)
			go func() {
				for i := 0; i < iterations; i++ {
					tasks <- engine.Task{Index: i + 1, Command: string(scriptContent)}
				}
				close(tasks)
			}()

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			opts := engine.ExecOptions{
				Verbose:    verboseFlag,
				Silent:     silentFlag,
				TotalTasks: iterations,
				Context:    ctx, Stdout: os.Stdout, Stderr: os.Stderr,
			}

			return engine.RunJobPool(actualConcurrency, tasks, opts)
		}

		return nil
	},
}

func init() {
	rootCmd.Flags().Bool("version", false, "Print the version number")
	rootCmd.Flags().BoolP("verbose", "v", false, "Show detailed per-execution information on stderr")
	rootCmd.Flags().Bool("silent", false, "Suppress child stdout/stderr payload")
	rootCmd.Flags().StringP("execute", "e", "", "Shell command or built-in to execute repeatedly")
	rootCmd.Flags().StringP("file", "f", "", "Path to shell script file to execute repeatedly")
	rootCmd.Flags().StringP("template", "t", "", "Path to Go template file defining the execution plan")
	rootCmd.Flags().IntP("concurrency", "c", 1, "Number of concurrent executions")
	rootCmd.Flags().IntP("iterations", "n", -1, "Total number of executions (defaults to concurrency)")
}

// Execute runs the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		if ve, ok := err.(*ValidationError); ok {
			fmt.Fprintln(os.Stderr, ve.Msg)
			os.Exit(ve.Code)
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
