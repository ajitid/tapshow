# Plan: switch tapshow input capture to a pkexec root helper

## Feasibility verdict

Yes, this is feasible and is the right shape for GNOME/Wayland.

Current tapshow opens `/dev/input/event*` directly in the GUI process:

- `cmd/tapshow/main.go:58` calls `input.NewReader().Start()`.
- `internal/input/reader.go` discovers keyboard devices with `/dev/input/event*`, opens them, reads Linux `input_event` structs, and converts them to `input.KeyEvent`.
- On this machine those devices are `root:input` with mode `0660`, and the current user is not in `input`, so direct mode will not work without broad input-device access.

Wayland GUI apps should not be run as root. The safe pattern is the same as Show Me The Key: keep GTK as the normal user, and run only a small input backend/helper as root via `pkexec`. `pkexec` supports matching a polkit action by installed program path plus first argv annotation (`org.freedesktop.policykit.exec.path` and `org.freedesktop.policykit.exec.argv1`; see `man pkexec`).

## Recommended product decision

Default runtime should use `pkexec` and should **not** require `input` group membership.

User decision: remove direct input mode from the normal CLI entirely. Keep direct device reading only as internal helper implementation code, not as a user-facing backend.

- `tapshow` -> launches `pkexec /path/to/tapshow input-helper`
- no `--input-backend=direct` option in the normal CLI

Rationale: this avoids a second supported user path and prevents silently reintroducing the permanent `input` group requirement.

## Architecture

Add a hidden helper subcommand:

```text
tapshow                       # normal GTK GUI process as user; always uses pkexec helper
tapshow input-helper          # hidden root-only helper, JSONL on stdout
```

Runtime flow:

```text
normal user GUI
  └─ input.PkexecReader.Start()
       └─ exec.Command("pkexec", absTapshowPath, "input-helper")
            └─ root helper
                 └─ input.DirectReader reads /dev/input/event*
                 └─ writes JSON lines: {code,name,state,timestamp}
```

The GUI process reads helper stdout, decodes JSON lines, and feeds the existing `processor.Processor`. The helper must write diagnostics to stderr only, never stdout, so stdout remains pure JSONL.

## Implementation steps

### 1. Refactor current reader into direct reader

File: `internal/input/reader.go`

Patch spec:

- Rename `type Reader` to `type DirectReader`.
- Rename methods:
  - `func NewReader() *Reader` -> `func NewDirectReader() *DirectReader`
  - receiver `(r *Reader)` -> `(r *DirectReader)` for `Events`, `Start`, `Stop`, `readDevice`
- Add a small interface near the top:

```go
type Reader interface {
	Events() <-chan KeyEvent
	Start() error
	Stop()
}
```

- Update error messages to mention direct mode instead of default app behavior:

```go
return fmt.Errorf("no keyboards found - direct input mode requires root or 'input' group access")
```

and

```go
return fmt.Errorf("could not open any keyboard devices - direct input mode requires root or 'input' group access")
```

No logic changes are needed inside device discovery/read parsing.

### 2. Add JSON event codec helpers

File: `internal/input/json.go` (new)

Create helpers that both GUI and helper use:

```go
package input

import (
	"encoding/json"
	"time"
)

type WireKeyEvent struct {
	Code      uint16   `json:"code"`
	Name      string   `json:"name"`
	State     KeyState `json:"state"`
	Timestamp int64    `json:"timestamp_unix_ms"`
}

func ToWire(ev KeyEvent) WireKeyEvent {
	return WireKeyEvent{Code: ev.Code, Name: ev.Name, State: ev.State, Timestamp: ev.Timestamp.UnixMilli()}
}

func FromWire(ev WireKeyEvent) KeyEvent {
	return KeyEvent{Code: ev.Code, Name: ev.Name, State: ev.State, Timestamp: time.UnixMilli(ev.Timestamp)}
}

func MarshalEvent(ev KeyEvent) ([]byte, error) {
	return json.Marshal(ToWire(ev))
}

func UnmarshalEvent(line []byte) (KeyEvent, error) {
	var wire WireKeyEvent
	if err := json.Unmarshal(line, &wire); err != nil {
		return KeyEvent{}, err
	}
	return FromWire(wire), nil
}
```

### 3. Add pkexec reader in the unprivileged process

File: `internal/input/pkexec_reader.go` (new)

Behavior:

- `NewPkexecReader(programPath string) *PkexecReader`
- `Start()` resolves the current binary path if `programPath == ""`:
  - first `os.Executable()`
  - `filepath.EvalSymlinks()` where possible
  - require absolute path, because `pkexec` policies match full paths
- launch `pkexec <absPath> input-helper`
- for installed usage, the resolved path should be `/usr/local/bin/tapshow`, matching README.md's existing install instruction (`sudo cp bin/tapshow /usr/local/bin/`) and the policy below
- attach stdout pipe and stderr pipe
- decode stdout line-by-line with `bufio.Scanner`
- send decoded `KeyEvent`s to `events`
- forward helper stderr to current stderr, prefixed with `tapshow input-helper:`
- on decode error or helper exit, close `events` and set an error field protected by mutex
- `Stop()` kills the helper process if still running and closes `done`

Skeleton:

```go
type PkexecReader struct {
	programPath string
	events chan KeyEvent
	done chan struct{}
	cmd *exec.Cmd
	mu sync.Mutex
	err error
}
```

Important implementation detail: `processor.Process` already exits when its input channel closes, but the app currently ignores reader failure after start. Add an `Err() error` method if desired, or at minimum log stderr and let app continue until user closes it. Prefer adding `Err()` only if the interface and main loop will use it.

### 4. Add hidden `input-helper` command

File: `cmd/tapshow/main.go`

Patch spec:

- Add command registration in `main()`:

```go
rootCmd.AddCommand(
	configCmd(),
	debugCmd(),
	versionCmd(),
	inputHelperCmd(),
)
```

- Add this function near `versionCmd()`:

```go
func inputHelperCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "input-helper",
		Short:  "Privileged input event helper",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInputHelper()
		},
	}
	return cmd
}

func runInputHelper() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("input-helper must be run as root via pkexec")
	}

	reader := input.NewDirectReader()
	if err := reader.Start(); err != nil {
		return err
	}
	defer reader.Stop()

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
```

Caveat: `DirectReader.Stop()` closes `done`; ensure it is not called twice in future changes.

### 5. Use pkexec reader unconditionally in GUI

File: `cmd/tapshow/main.go`

Patch spec:

- Do not add a user-facing `--input-backend` flag.
- Replace current reader construction in `run()`:

Current:

```go
reader := input.NewReader()
if err := reader.Start(); err != nil {
	return fmt.Errorf("starting input reader: %w", err)
}
defer reader.Stop()
```

New:

```go
reader := input.NewPkexecReader("")
if err := reader.Start(); err != nil {
	return fmt.Errorf("starting input reader: %w", err)
}
defer reader.Stop()
```

The old direct reader is used only inside `runInputHelper()`.

### 6. Add polkit policy file

File: `packaging/ca.icewolf.tapshow.policy` (new)

Use the same app id as the GTK app currently uses in `internal/display/gtkwindow.go` (`ca.icewolf.tapshow`). Example:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE policyconfig PUBLIC "-//freedesktop//DTD PolicyKit Policy Configuration 1.0//EN" "http://www.freedesktop.org/standards/PolicyKit/1/policyconfig.dtd">
<policyconfig>
  <vendor>tapshow</vendor>
  <vendor_url>https://github.com/hmnd/tapshow</vendor_url>
  <action id="ca.icewolf.tapshow.input-helper">
    <description>Read keyboard events for tapshow</description>
    <message>Authentication is required to read keyboard events for tapshow</message>
    <defaults>
      <allow_any>auth_admin_keep</allow_any>
      <allow_inactive>auth_admin_keep</allow_inactive>
      <allow_active>auth_admin_keep</allow_active>
    </defaults>
    <annotate key="org.freedesktop.policykit.exec.path">/usr/local/bin/tapshow</annotate>
    <annotate key="org.freedesktop.policykit.exec.argv1">input-helper</annotate>
  </action>
</policyconfig>
```

Installation path: `/usr/share/polkit-1/actions/ca.icewolf.tapshow.policy`.

Important: the `exec.path` must match the installed binary. README.md currently installs with `sudo cp bin/tapshow /usr/local/bin/`, so this plan targets `/usr/local/bin/tapshow`.

### 7. Update build/install tasks

File: `justfile`

Patch spec:

- Keep `build` unchanged.
- Add install target:

```just
install: build
    sudo install -Dm755 bin/tapshow /usr/local/bin/tapshow
    sudo install -Dm644 packaging/ca.icewolf.tapshow.policy /usr/share/polkit-1/actions/ca.icewolf.tapshow.policy
```

Do not add a `run-direct` target or user-facing direct mode.

### 8. Update README

File: `README.md`

Patch spec:

- Replace the `Input Group Membership` prerequisite section with `Privileged input helper`.
- Explain:
  - default mode uses `pkexec` and does not require `input` group membership
  - a polkit authentication prompt is expected
  - installing the policy makes the prompt specific to tapshow
  - direct `/dev/input` mode is no longer user-facing; it is only used internally by the privileged helper
- Update troubleshooting:
  - `pkexec` missing: install polkit
  - authentication denied/cancelled: rerun and authenticate
  - policy path mismatch: verify `which tapshow` and the `org.freedesktop.policykit.exec.path` annotation
  - no keyboards found from helper: try `sudo /usr/bin/tapshow input-helper` to isolate device discovery

### 9. Tests

Add tests without requiring root:

File: `internal/input/json_test.go` (new)

- round-trip `KeyEvent -> JSON -> KeyEvent`
- verify state/code/name/timestamp survive

File: `cmd/tapshow/main_test.go` or avoid command tests if importing GTK in tests is too slow. If testing main is problematic due GTK dependency, keep coverage in `internal/input` and manually verify CLI behavior.

Manual verification commands:

```bash
cd /home/ajit/ghq/github.com/hmnd/tapshow
go test ./internal/input ./internal/processor ./internal/config ./internal/privacy
just build
./bin/tapshow input-helper              # should fail: must be root via pkexec
sudo ./bin/tapshow input-helper         # should print JSONL for key events; Ctrl+C to stop
sudo install -Dm755 bin/tapshow /usr/local/bin/tapshow
sudo install -Dm644 packaging/ca.icewolf.tapshow.policy /usr/share/polkit-1/actions/ca.icewolf.tapshow.policy
/usr/local/bin/tapshow                  # should show pkexec auth, then GUI overlay
```

On this machine, `go test ./...` initially timed out while downloading/building dependencies, so allow a longer timeout on first full run.

## Security notes

- The helper should only emit key events to stdout and read no user-controlled paths.
- The helper must not run the GTK app as root.
- Do not preserve GUI-related environment variables for pkexec; no `allow_gui` annotation is needed.
- A custom policy can authorize only `/usr/bin/tapshow input-helper` via `argv1`; it should not authorize arbitrary tapshow subcommands.
- Since helper stdout contains raw key events, ensure it is connected only to the parent process pipe.

## Open questions / decisions before implementation

Resolved decisions:

1. Direct input mode should be removed from the normal CLI. Direct device reading remains only as the hidden helper implementation.
2. The policy/install target should be `/usr/local/bin/tapshow`, because README.md currently says `sudo cp bin/tapshow /usr/local/bin/`.
3. Polkit should allow cached admin authorization with `auth_admin_keep`.

## References / inspiration

- Show Me The Key project website: <https://showmethekey.alynx.one/>
  - It explicitly supports Wayland by reading key events via `libinput` instead of the display protocol.
  - Its usage notes explain that the user toggles the app and authenticates through `pkexec` because the backend needs elevated permission to read keyboard events.
  - Its FAQ explains why `/dev`/evdev access needs root and why this differs from X11 screenkey.
- Show Me The Key README: <https://github.com/AlynxZhou/showmethekey/blob/master/README.md>
  - `Project Structure -> GTK` describes the GUI frontend running a CLI backend as root via `pkexec` and showing a transparent floating window.
  - `Project Structure -> CLI` describes Show Me The Key's own privileged backend implementation using `libinput`, `libudev`, and `libevdev`; tapshow will **not** adopt those libraries in this plan. Tapshow will keep its existing direct `/dev/input/event*` Go/syscall reader and move only that reader behind a `pkexec` helper.
- `man pkexec` on this system:
  - Documents `org.freedesktop.policykit.exec.path` and `org.freedesktop.policykit.exec.argv1`, which this plan uses to authorize only the hidden `input-helper` subcommand.
