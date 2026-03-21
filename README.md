# coxec

`coxec` is a concurrent execution engine with built-in protocol clients, templates, pipeline chaining, and native AI agent support.

## Features

- Run `N` executions with `C` concurrent workers.
- **Timing Control**: Set individual (`--timeout`) and global (`--global-timeout`) limits.
- **Traffic Shaping**: Stagger worker starts with `--delay`, add `--jitter`, or use `--rampup`.
- **Throttling**: Enforce maximum execution rates with `--rate` (e.g., `50/s`, `100/m`).
- **Server Mode**: Start `coxec` as an HTTP execution service with `-s/--server`.
- Powerful Go Template support in all execution modes.
- Generate random test data (names, emails, phones, numbers).
- Drive iterations from data files or sequences.
- Per-execution verbose logging (duration, exit status) to stderr.
- Stable per-run env var: `COXEC_INDEX=1..N`.
- Exit codes that distinguish `all-failed`, `partially-failed`, and `timeout` (124) runs.

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

One of `-e/--exec`, `-f/--file`, `-t/--template`, or `-s/--server` is required.

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
.http GET https://api.example.com/users/{{randInt 1 100}}
  |> .http POST https://api.example.com/logs
     --body {"user_id": "{{.Prev.ID}}", "status": "verified"}
```

```bash
coxec -t smoke-test.tmpl -c 10 -n 100
```

### Server Mode
Start `coxec` as a long-running HTTP service:

```bash
coxec --server --port 9000
```

#### Health Check
Verify the server status using the `/health` endpoint:

```bash
curl http://localhost:8080/health
```

**Response:**
```json
{
  "status": "ok",
  "version": "1.0.0",
  "active_jobs": 2,
  "uptime_seconds": 3600
}
```

The endpoint returns `503 Service Unavailable` if the server is starting up or shutting down.

## Flags

- `-e, --exec string`: Shell command to execute repeatedly.
- `-f, --file string`: Path to a file whose contents will be executed repeatedly.
- `-t, --template string`: Path to a Go template file defining the execution plan.
- `-s, --server`: Start `coxec` in server mode.
- `-a, --addr string`: Bind address for the server (default: `127.0.0.1`).
- `-p, --port int`: Port to listen on (default: `8080`).
- `-c, --concurrency int`: Number of concurrent workers. (default: `1`)
- `-n, --iterations int`: Total number of executions (defaults to `--concurrency`).
- `--rate string`: Maximum execution rate (e.g., `50/s`, `10/m`, `1/h`).
- `--timeout duration`: Max time for a *single* execution (e.g., `5s`, `500ms`).
- `--global-timeout duration`: Max time for the *entire* run (e.g., `1h`, `15m`).
- `--delay duration`: Fixed delay between starting each worker/iteration.
- `--jitter duration`: Random variation added to delay (delay ± jitter).
- `--rampup duration`: Gradually increase concurrency over this period.
- `--var key=value`: Set a user variable available as `{{.Var "key"}}`.
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
coxec -c 10 -n 100 -e '.http POST https://api.example.com/users --body {"name": "{{randName}}", "email": "{{randEmail}}", "phone": "{{randPhone}}"}'
```

**Sequential Batch Processing**:
```bash
coxec -c 5 -n 1000 -e '.http PUT https://api.example.com/items/{{fileLine "ids.txt"}} --body {"status": "active"}'
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

## Timing & Flow Control

`coxec` provides precise control over how and when executions happen.

### Rate Limiting
Limit throughput to avoid overwhelming target systems:
```bash
# Aim for 50 requests per second across 20 workers
coxec -c 20 -n 1000 --rate 50/s -e '.http GET https://api.example.com'
```

### Warm-up (Ramp-up)
Avoid thundering herds by gradually increasing concurrency:
```bash
# Start 1 worker every 3 seconds until 10 are active
coxec -c 10 -n 100 --rampup 30s -e '.http GET https://api.example.com'
```

### Timeouts
Protect against hanging processes or slow APIs:
```bash
# Kill any task taking longer than 2s; stop the whole run after 5m
coxec -n 100 --timeout 2s --global-timeout 5m -e 'sleep 10'
```

### Staggered Starts
Add spacing and jitter between starts for realistic simulations:
```bash
# Space starts by 500ms ± 100ms
coxec -c 5 -n 20 --delay 500ms --jitter 100ms -e 'echo "Starting..."'
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
