<h1 align=center>tapshow</h1>

`tapshow` is a lightweight keystroke visualizer for Wayland systems. Displays your keystrokes as a minimal overlay window - perfect for screen recordings, presentations, and live coding!

<p align="center">
  <img title="Screenshot of tapshow in action" src="assets/example.png" />
</p>

## Features

- Real-time keystroke visualization
- Modifier key combination display (e.g., `Ctrl+Shift+A`)
- Privacy mode - auto-pause for sensitive applications
- Inherits your GTK styles

## Compositor Support

| Compositor | Support | Notes                                                                                 |
| ---------- | ------- | ------------------------------------------------------------------------------------- |
| Sway       | Yes     |                                                                                       |
| Hyprland   | Yes     |                                                                                       |
| KDE Plasma | Yes     | To display above other windows: right-click window > More Actions > Keep Above Others |
| GNOME      | Yes     | To display above other windows: right-click window > Always on Top                    |

## Prerequisites

### System Dependencies

```bash
# Debian/Ubuntu
sudo apt install libgtk-4-dev libglib2.0-dev libgirepository1.0-dev

# Fedora
sudo dnf install gtk4-devel glib2-devel gobject-introspection-devel

# Arch Linux
sudo pacman -S gtk4 glib2 gobject-introspection-runtime
```

### Input permissions

Tapshow reads keyboard events from `/dev/input/event*`.

If your user can already read those devices, for example via the `input` group or equivalent udev permissions, tapshow reads them directly and no polkit prompt is shown.

Otherwise tapshow uses `pkexec` to run a small privileged input helper. The GTK app still runs as your normal user; only the helper reads `/dev/input/event*` as root.

Installing the polkit policy makes the authentication prompt specific to tapshow and authorizes only the helper subcommand.

## Installation

### From Release

[Download the latest release](https://github.com/hmnd/tapshow/releases/latest)

### From Source

```bash
# Clone repository
git clone https://github.com/tapshow/tapshow.git
cd tapshow

# Build (requires zig for C compilation)
just build

# Install (optional)
just install
```

## Usage

```bash
# Run tapshow
tapshow

# Show config file location
tapshow config path

# Create default config file
tapshow config init

# Continuously log active app to help with pause_on_apps
tapshow debug active-app

# Show version
tapshow version
```

## Configuration

Run `tapshow config init` or refer to [the default config](configs/default.toml)

## Privacy Mode

Tapshow can automatically pause when sensitive applications are focused. Add application names to `pause_on_apps` in your config:

```toml
[privacy]
pause_on_apps = [
  "simple-match",
  { class = "org.keepassxc" },
  { process = "1password", title = "unlock" },
]
```

The privacy monitor checks the focused window every 500ms and pauses the display when a matching app name is detected.

## Troubleshooting

### `pkexec` Missing / Authentication Prompt Appears

Install polkit for your distribution, then run tapshow again.

If you do not want a polkit prompt, grant your user read access to keyboard event devices, commonly by adding the user to the `input` group and logging out/in. Be aware that this grants broad input-device access.

### Authentication Denied or Cancelled

Run tapshow again and complete the polkit authentication prompt.

### Policy Path Mismatch

The packaged policy authorizes `/usr/local/bin/tapshow input-helper`. Verify the installed path:

```bash
which tapshow
```

If it differs, update the `org.freedesktop.policykit.exec.path` annotation in `/usr/share/polkit-1/actions/ca.icewolf.tapshow.policy` or install tapshow to `/usr/local/bin/tapshow`.

### "no keyboards found" from Helper

Run the helper directly as root to isolate device discovery:

```bash
sudo /usr/local/bin/tapshow input-helper
```

### Window Not Visible

1. Verify Wayland session: `echo $XDG_SESSION_TYPE` should output `wayland`
2. For GNOME/KDE, set the window to stay on top (see compositor support table)

### Keys Not Appearing

1. Check that tapshow is running: `pgrep tapshow`
2. Verify direct input access: `test -r /dev/input/event0` is only a rough check; device numbers vary.
3. Verify the helper can discover keyboards: `sudo /usr/local/bin/tapshow input-helper`
4. Verify the polkit policy path matches the installed binary

## Building from Source

### Requirements

- Go 1.25+
- GTK4 development libraries
- GLib development libraries
- Zig (for C/C++ compilation)
- Just (command runner)

### Build

```bash
just build
```

## License

MIT License - see LICENSE file for details.
