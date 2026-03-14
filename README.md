# coxec

`coxec` is a concurrent execution engine with built-in protocol clients, templates, pipeline chaining, and native AI agent support.

## Features

- Run `N` executions with `C` concurrent workers.
- Per-execution verbose logging (duration, exit status) to stderr.
- Stable per-run env var: `COXEC_INDEX=1..N`.
- Exit codes that distinguish `all-failed` vs `partially-failed` runs.

## Install

### From source (local)

```sh
make build
./bin/coxec --version
```

### With Go

```sh
go install github.com/0funct0ry/coxec@latest
coxec --version
```

## Usage

One of `-e/--exec` or `-f/--file` is required (exactly one). `-t/--template` is currently accepted by flag parsing but not implemented yet.

```sh
coxec -e 'echo "hello from $COXEC_INDEX"' -c 4 -n 10
```

### Common examples

Run a command 100 times with concurrency 20:

```sh
coxec -e 'curl -fsS https://example.com/health' -c 20 -n 100
```

Run with per-execution timings and exit status (verbose output goes to stderr):

```sh
coxec -e 'sleep 0.1' -c 8 -n 50 -v
```

Execute the contents of a script file repeatedly:

```sh
coxec -f ./test.sh -c 4 -n 12
```

Suppress child stdout/stderr payload (summary still prints on stderr):

```sh
coxec -e 'echo hi' -c 4 -n 20 --silent
```

Hide the summary/diagnostics by redirecting stderr:

```sh
coxec -e 'echo hi' -c 2 -n 4 2>/dev/null
```

## Flags

- `-e, --exec string`: Shell command to execute repeatedly.
- `-f, --file string`: Path to a file whose contents will be executed repeatedly.
- `-t, --template string`: Reserved for future use (currently not implemented).
- `-c, --concurrency int`: Number of concurrent executions. (default: `1`)
- `-n, --iterations int`: Total number of executions (defaults to `--concurrency` when not provided; `-n 0` runs zero executions and exits successfully).
- `-v, --verbose`: Print per-execution timing and status to stderr.
- `--silent`: Suppress the child stdout/stderr payload.
- `--version`: Print the version and exit.

## Execution model and shell notes

- Each execution runs as `sh -c <command>` (for bash features: `coxec -e "bash -lc '...'" ...`).
- For `-f/--file`, `coxec` reads the file contents and passes them to `sh -c` (the file's shebang is not used).

## Output behavior

- Child stdout is written to stdout (unless `--silent`).
- Summary and verbose diagnostics are written to stderr.

## Exit codes

- `0`: all executions succeeded (or `-n 0`).
- `1`: partial failure (some executions failed, some succeeded).
- `2`: all executions failed.
- `64`: CLI validation error (for example, missing `-e/-f`).
- `130`: interrupted (Ctrl-C / SIGTERM).
- `10`: other unexpected error.

## Development

```sh
make lint
make test
make build
```

## License

MIT. See `LICENSE`.
