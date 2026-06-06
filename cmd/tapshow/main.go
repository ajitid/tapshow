package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tapshow/tapshow/internal/config"
	"github.com/tapshow/tapshow/internal/display"
	"github.com/tapshow/tapshow/internal/input"
	"github.com/tapshow/tapshow/internal/privacy"
	"github.com/tapshow/tapshow/internal/processor"
)

var version = "dev"

func main() {
	rootCmd := &cobra.Command{
		Use:   "tapshow",
		Short: "Keystroke visualizer for Wayland",
		Long: `tapshow displays your keystrokes as a minimal overlay window.
Designed for screen recordings, presentations, and live coding.`,
		RunE: run,
	}

	rootCmd.AddCommand(
		configCmd(),
		debugCmd(),
		versionCmd(),
		inputHelperCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	compositor := display.Detect()
	fmt.Printf("Detected compositor: %s\n", compositor)

	backend := display.New()
	fmt.Println("Using GTK window backend")
	showGTKWindowTips(compositor)

	if err := backend.Init(cfg); err != nil {
		return fmt.Errorf("initializing display: %w", err)
	}

	var reader input.Reader
	directReader := input.NewDirectReader()
	if err := directReader.Start(); err == nil {
		reader = directReader
		fmt.Println("Using direct input backend")
	} else {
		fmt.Printf("Direct input unavailable (%v); falling back to pkexec.\n", err)
		pkexecReader := input.NewPkexecReader("")
		if err := pkexecReader.Start(); err != nil {
			return fmt.Errorf("starting input reader: %w", err)
		}
		reader = pkexecReader
		fmt.Println("Using pkexec input backend")
	}
	defer reader.Stop()

	procCfg := processor.Config{
		CombineModifiers: cfg.Behavior.CombineModifiers,
		ShowModifierOnly: cfg.Behavior.ShowModifierOnly,
		ShowHeldKeys:     cfg.Display.ShowHeldKeys,
		HeldKeyTimeout:   cfg.HeldKeyTimeout(),
		ResetTimeout:     cfg.Timeout(),
		HistoryCount:     cfg.Display.HistoryCount,
		ExcludedKeys:     cfg.Behavior.ExcludedKeys,
	}
	proc := processor.New(procCfg)

	processorInput := make(chan input.KeyEvent, 100)
	readerErr := make(chan error, 1)
	go func() {
		defer close(processorInput)
		for ev := range reader.Events() {
			processorInput <- ev
		}
		if errReader, ok := reader.(interface{ Err() error }); ok {
			readerErr <- errReader.Err()
			return
		}
		readerErr <- nil
	}()

	go proc.Process(processorInput)
	defer proc.Stop()

	privacyMonitor := privacy.NewMonitor(cfg.Privacy.PauseOnApps, func(paused bool) {
		backend.SetPaused(paused)
		if paused {
			fmt.Println("Privacy: paused (sensitive app focused)")
		} else {
			fmt.Println("Privacy: resumed")
		}
	})
	privacyMonitor.Start()
	defer privacyMonitor.Stop()

	go func() {
		for event := range proc.Events() {
			if event.IsReset {
				backend.Reset()
			} else {
				backend.Show(event)
				backend.UpdateHistory(proc.History())
			}
		}
	}()

	var stopBackendOnce sync.Once
	stopBackend := func() {
		stopBackendOnce.Do(func() {
			backend.Stop()
		})
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nShutting down...")
		stopBackend()
	}()

	runErr := make(chan error, 1)
	go func() {
		if err := <-readerErr; err != nil {
			fmt.Fprintf(os.Stderr, "Input reader failed: %v\n", err)
			runErr <- err
			stopBackend()
		}
	}()

	fmt.Println("tapshow running. Press Ctrl+C to exit.")
	if err := backend.Run(); err != nil {
		return err
	}
	select {
	case err := <-runErr:
		return fmt.Errorf("input reader failed: %w", err)
	default:
		return nil
	}
}

func showGTKWindowTips(compositor display.Compositor) {
	switch compositor {
	case display.CompositorKDE:
		fmt.Println("Tip: Right-click the window → More Actions → Keep Above Others")
	case display.CompositorGNOME:
		fmt.Println("Tip: Right-click the window → Always on Top")
	}
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration management",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "path",
			Short: "Print the configuration file path",
			RunE: func(cmd *cobra.Command, args []string) error {
				path, err := config.Path()
				if err != nil {
					return err
				}
				fmt.Println(path)
				return nil
			},
		},
		&cobra.Command{
			Use:   "init",
			Short: "Create a default configuration file",
			RunE: func(cmd *cobra.Command, args []string) error {
				path, err := config.Path()
				if err != nil {
					return err
				}

				if _, err := os.Stat(path); err == nil {
					return fmt.Errorf("config already exists at %s", path)
				}

				cfg := config.Default()
				if err := cfg.Save(); err != nil {
					return err
				}

				fmt.Printf("Created config at: %s\n", path)
				return nil
			},
		},
	)

	return cmd
}

func debugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Debugging utilities",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "active-app",
			Short: "Continuously print the focused window info as it changes",
			Long:  "Continuously print the focused window info as it changes. Useful for finding apps to add to privacy.pause_on_apps config",
			Run: func(cmd *cobra.Command, args []string) {
				compositor := display.Detect()
				fmt.Printf("Detected compositor: %s\n", compositor)
				fmt.Println("Watching for focus changes... (Ctrl+C to exit)")
				fmt.Println()

				sigChan := make(chan os.Signal, 1)
				signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

				ticker := time.NewTicker(500 * time.Millisecond)
				defer ticker.Stop()

				var lastInfo string
				for {
					select {
					case <-sigChan:
						fmt.Println("\nStopped.")
						return
					case <-ticker.C:
						info := privacy.GetFocusedWindow(compositor)
						infoStr := info.String()
						if infoStr != lastInfo {
							lastInfo = infoStr
							if info.IsEmpty() {
								fmt.Println("(no focused window)")
							} else {
								fmt.Println(infoStr)
							}
						}
					}
				}
			},
		},
	)

	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("tapshow %s\n", version)
		},
	}
}

func inputHelperCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "input-helper",
		Short:  "Privileged input event helper",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInputHelper()
		},
	}
}

func runInputHelper() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("input-helper must be run as root")
	}

	reader := input.NewDirectReader()
	if err := reader.Start(); err != nil {
		return err
	}
	defer reader.Stop()

	ready, err := input.MarshalReady()
	if err != nil {
		return fmt.Errorf("marshal ready message: %w", err)
	}
	if _, err := os.Stdout.Write(append(ready, '\n')); err != nil {
		return fmt.Errorf("write ready message: %w", err)
	}

	for ev := range reader.Events() {
		line, err := input.MarshalEvent(ev)
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal event: %v\n", err)
			continue
		}
		if _, err := os.Stdout.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}
