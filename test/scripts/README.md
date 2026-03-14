# `coxec` Test Scripts

A collection of scripts designed to test various aspects of the `coxec` tool's `-f` flag functionality.

## Table of Contents

| Script Name                        | Description                                                                |
|------------------------------------|----------------------------------------------------------------------------|
| [`basic.sh`](#basicsh)             | Tests basic arithmetic and environment variable availability               |
| [`env.sh`](#envsh)                 | Tests basic environment variable availability and simple command execution |
| [`math.sh`](#mathsh)               | Tests arithmetic operations and conditional logic within scripts           |
| [`fileops.sh`](#fileopssh)         | Tests file system operations and temporary file handling                   |
| [`network.sh`](#networksh)         | Tests network connectivity and external service calls                      |
| [`conditional.sh`](#conditionalsh) | Tests failure handling and conditional execution paths                     |
| [`fail.sh`](#failsh)               | Tests failure detection with a specific exit code                          |
| [`data.sh`](#datash)               | Tests multi-step processes with file I/O and logging                       |
| [`api.sh`](#apish)                 | Tests variable timing and simulated API interactions                       |
| [`log.sh`](#logsh)                 | Tests complex output formatting and conditional exit codes                 |
| [`slow.sh`](#slowsh)               | Tests concurrency and timing with intentional delays                       |
| [`resource.sh`](#resourcesh)       | Tests system command integration and information gathering                 |
| [`noexec.sh`](#noexecsh)           | Tests execution of scripts without execution permissions                   |
| [`workflow.sh`](#workflowsh)       | Tests multi-stage workflows with validation and error handling             |

## Script Details

### `basic.sh`
A fundamental test script that performs simple arithmetic and checks for environment variable presence. Useful for initial verification of the tool's core functionality.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/basic.sh -n 5 -c 2
```

### `env.sh`
Tests that the `COXEC_INDEX` environment variable is properly set and available to executed scripts. Verifies basic command execution and output formatting.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/env.sh -n 10 -c 5
```


### `math.sh`
Tests arithmetic operations, variable assignments, and conditional statements within the execution context. Ensures mathematical operations work correctly across multiple executions.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/math.sh -n 8 -c 4
```

### `fileops.sh`
Tests file system operations including creating, reading, and deleting files. Validates that each execution gets its own unique temporary file without conflicts.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/fileops.sh -n 5 -c 2
```

### `network.sh`
Tests external network connectivity and measures response times. Useful for verifying concurrent execution behavior and timeout handling.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/network.sh -n 6 -c 3
```

### `conditional.sh`
Tests failure handling by intentionally causing failures on specific execution indices. Verifies that the coxec tool properly counts and reports failures.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/conditional.sh -n 15 -c 5
```

### `fail.sh`
Tests the tool's ability to handle and report non-zero exit codes. This script always exits with code 42.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/fail.sh -n 3 -c 1
```

### `data.sh`
Tests more complex, multi-step processes that involve file I/O and temporary storage. Simulates real-world data processing pipelines.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/data.sh -n 4 -c 2
```

### `api.sh`
Tests variable execution times and simulates API calls with different response characteristics. Useful for concurrency testing.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/api.sh -n 12 -c 6
```

### `log.sh`
Tests complex output formatting, conditional logic, and variable logging levels. Includes both successful and failed execution paths.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/log.sh -n 20 -c 10
```

### `slow.sh`
Introduces an artificial delay (0.5 seconds) to each execution. Used to test the tool's performance and concurrency under realistic load.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/slow.sh -n 10 -c 5
```

### `resource.sh`
Tests system-level commands and information gathering. Useful for verifying that system commands execute properly in the coxec environment.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/resource.sh -n 3 -c 1
```

### `noexec.sh`
A simple script that can be used to test the execution behavior of scripts that may not have explicit execute permissions or when they are executed through specific shells.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/noexec.sh -n 1 -c 1
```

### `workflow.sh`
Tests comprehensive workflow patterns with multiple validation steps, processing stages, and error handling mechanisms.

**Usage Example:**
```bash
./bin/coxec -f test/scripts/workflow.sh -n 7 -c 3
```

