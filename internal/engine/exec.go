package engine

import (
	"os"
	"os/exec"
	"sync"
)

var outputMu sync.Mutex

// RunShellCommand executes a single command in the default shell
// Captures output and prints serially to avoid interleaved characters
func RunShellCommand(command string) error {
	cmd := exec.Command("sh", "-c", command)
	
	output, err := cmd.CombinedOutput()
	
	outputMu.Lock()
	defer outputMu.Unlock()
	
	if len(output) > 0 {
		os.Stdout.Write(output)
	}
	
	return err
}

// RunJobPool executes commands from the tasks channel across a pool of worker goroutines
func RunJobPool(concurrency int, tasks <-chan string) error {
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for cmd := range tasks {
				if err := RunShellCommand(cmd); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
				}
			}
		}()
	}

	wg.Wait()
	return firstErr
}
