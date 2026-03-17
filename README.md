# coxec

`coxec` is a concurrent execution engine with built-in protocol clients, templates, pipeline chaining, and native AI agent support.

## Features

- Run `N` executions with `C` concurrent workers.
- Powerful Go Template support in all execution modes.
- Generate random test data (names, emails, phones, numbers).
- Drive iterations from data files or sequences.
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

One of `-e/--exec`, `-f/--file`, or `-t/--template` is required (exactly one).

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

### Advanced Templates
Use `-t` for multi-step execution plans or complex data handling:

```gotemplate
# smoke-test.tmpl
http GET https://api.example.com/users/{{randInt 1 100}}
  |> http POST https://api.example.com/logs
     --body {"user_id": "{{.Prev.ID}}", "status": "verified"}
```

```bash
coxec -t smoke-test.tmpl -c 10 -n 100
```

## Flags

- `-e, --exec string`: Shell command to execute repeatedly.
- `-f, --file string`: Path to a file whose contents will be executed repeatedly.
- `-t, --template string`: Path to a Go template file defining the execution plan.
- `-c, --concurrency int`: Number of concurrent executions. (default: `1`)
- `-n, --iterations int`: Total number of executions (defaults to `--concurrency` when not provided).
- `--var key=value`: Set a user variable available as `{{.Var "key"}}`. Can be repeated.
- `-v, --verbose`: Print per-execution timing and status to stderr.
- `--silent`: Suppress the child stdout/stderr payload.
- `--version`: Print the version and exit.

## Execution model and shell notes

- Each execution runs as `sh -c <command>`.
- For `-f/--file`, `coxec` renders the template then passes the result to `sh -c`.
- For `-t/--template`, `coxec` executes the plan natively (built-ins or shell fallthrough).

## Template Engine

All execution flags support Go templates (`text/template`) with the following context:

### Context Variables
- `.Iteration`: 0-based iteration number.
- `.WorkerID`: ID of the worker (goroutine) executing the task.
- `.Timestamp`: Execution start time (RFC3339 with ms).
- `.TimestampUnix/Milli/Nano`: Numeric timestamps.
- `.UUID`: Unique ID for the execution.
- `{{.Env "KEY"}}`: Get environment variable.
- `{{.Var "KEY"}}`: Get user variable from `--var` (falls back to Env).
- `.Prev`: Result object from the previous pipeline step.

### Functions
- **Formatting**: `quote` (shell escaping).
- **Random Data**: `randInt min max`, `randFloat min max dec`, `randString len`, `randChoice "a" "b"`.
- **Identity**: `randName`, `randEmail`, `randPhone`, `uuid`, `ulid`.
- **Data Driven**:
    - `seq start end step`: Generate iteration-based sequence.
    - `counter "name"`: Shared incrementing counter.
    - `fileLine "data.txt"`: Sequential line access (wraps).
    - `fileLineAt "data.txt" 1`: 1-based line access.

### Examples

**Random Load Generation**:
```bash
coxec -c 10 -n 100 -e 'http POST https://api.example.com/users --body {"name": "{{randName}}", "email": "{{randEmail}}", "phone": "{{randPhone}}"}'
```

**Sequential Batch Processing**:
```bash
coxec -c 5 -n 1000 -e 'http PUT https://api.example.com/items/{{fileLine "ids.txt"}} --body {"status": "active"}'
```

**Custom Identification**:
```bash
coxec -c 20 -n 100 -e 'echo "Starting iteration {{counter \"job\"}} with ID {{ulid}}"'
```

**Sequence Generation**:
```bash
# Generate requests for pages 10, 15, 20, 25, 30
coxec -n 5 -e 'curl https://api.example.com/search?page={{seq 10 100 5}}'
```

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
