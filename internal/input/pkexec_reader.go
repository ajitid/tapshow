package input

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

type PkexecReader struct {
	programPath string
	events      chan KeyEvent
	done        chan struct{}
	cmd         *exec.Cmd
	mu          sync.Mutex
	err         error
	stopOnce    sync.Once
}

func NewPkexecReader(programPath string) *PkexecReader {
	return &PkexecReader{
		programPath: programPath,
		events:      make(chan KeyEvent, 100),
		done:        make(chan struct{}),
	}
}

func (r *PkexecReader) Events() <-chan KeyEvent {
	return r.events
}

func (r *PkexecReader) Start() error {
	programPath, err := resolveProgramPath(r.programPath)
	if err != nil {
		return err
	}

	cmd := exec.Command("pkexec", programPath, "input-helper")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("input helper stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("input helper stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting pkexec input helper: %w", err)
	}

	r.cmd = cmd
	go r.forwardStderr(stderr)

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("read input helper startup: %w", err)
		}
		if err := cmd.Wait(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("input helper startup failed: %w", err)
		}
		return fmt.Errorf("input helper exited before startup confirmation")
	}

	ready, err := IsReady(scanner.Bytes())
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("decode input helper startup: %w", err)
	}
	if !ready {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("input helper sent unexpected startup message")
	}

	go r.readStdout(scanner)

	return nil
}

func (r *PkexecReader) Stop() {
	r.stopOnce.Do(func() {
		close(r.done)
		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
	})
}

func (r *PkexecReader) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *PkexecReader) setErr(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		r.err = err
	}
}

func (r *PkexecReader) readStdout(scanner *bufio.Scanner) {
	defer close(r.events)

	for scanner.Scan() {
		ev, err := UnmarshalEvent(scanner.Bytes())
		if err != nil {
			r.setErr(fmt.Errorf("decode input helper event: %w", err))
			if r.cmd != nil && r.cmd.Process != nil {
				_ = r.cmd.Process.Kill()
			}
			break
		}

		select {
		case r.events <- ev:
		case <-r.done:
			return
		}
	}

	select {
	case <-r.done:
		return
	default:
	}

	if err := scanner.Err(); err != nil {
		r.setErr(fmt.Errorf("read input helper stdout: %w", err))
	}

	if r.cmd != nil {
		if err := r.cmd.Wait(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			r.setErr(fmt.Errorf("input helper exited: %w", err))
		} else if r.Err() == nil {
			r.setErr(fmt.Errorf("input helper exited"))
		}
	}
}

func (r *PkexecReader) forwardStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		fmt.Fprintf(os.Stderr, "tapshow input-helper: %s\n", scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "tapshow input-helper: reading stderr: %v\n", err)
	}
}

func resolveProgramPath(programPath string) (string, error) {
	if programPath == "" {
		path, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("resolve executable path: %w", err)
		}
		programPath = path
	}

	if resolved, err := filepath.EvalSymlinks(programPath); err == nil {
		programPath = resolved
	}

	if !filepath.IsAbs(programPath) {
		return "", fmt.Errorf("pkexec input helper requires an absolute program path, got %q", programPath)
	}
	return programPath, nil
}
