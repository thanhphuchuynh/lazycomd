#!/bin/sh
# Install lazycomd to ~/.local/bin, enable login autostart, and start it now.
set -e

REPO=thanhphuchuynh/lazycomd
BIN_DIR="${LAZYCOMD_BIN_DIR:-$HOME/.local/bin}"
BIN="$BIN_DIR/lazycomd"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
	x86_64) arch=amd64 ;;
	aarch64|arm64) arch=arm64 ;;
	*)
		echo "lazycomd: unsupported architecture $arch" >&2
		exit 1
		;;
esac
case "$os" in
	linux|darwin) ;;
	*)
		echo "lazycomd: unsupported OS $os" >&2
		exit 1
		;;
esac

tag=${LAZYCOMD_VERSION:-latest}
if [ "$tag" = latest ]; then
	url="https://github.com/$REPO/releases/latest/download/lazycomd_${os}_${arch}"
else
	url="https://github.com/$REPO/releases/download/$tag/lazycomd_${os}_${arch}"
fi

mkdir -p "$BIN_DIR"
echo "downloading $url"
curl -sSfL "$url" -o "$BIN"
chmod 755 "$BIN"

install_launchd() {
	plist="$HOME/Library/LaunchAgents/com.tphuc.lazycomd.plist"
	mkdir -p "$HOME/Library/LaunchAgents"
	uid=$(id -u)
	cat >"$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.tphuc.lazycomd</string>
  <key>ProgramArguments</key>
  <array>
    <string>$BIN</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$HOME/.local/state/lazycomd/serve.log</string>
  <key>StandardErrorPath</key>
  <string>$HOME/.local/state/lazycomd/serve.log</string>
</dict>
</plist>
EOF
	mkdir -p "$HOME/.local/state/lazycomd"
	target="gui/$uid/com.tphuc.lazycomd"
	launchctl bootout "$target" 2>/dev/null || true
	if launchctl bootstrap "gui/$uid" "$plist" 2>/dev/null; then
		launchctl enable "$target" 2>/dev/null || true
		launchctl kickstart -k "$target"
	else
		launchctl load -w "$plist"
	fi
}

install_systemd() {
	unit_dir="$HOME/.config/systemd/user"
	mkdir -p "$unit_dir"
	cat >"$unit_dir/lazycomd.service" <<EOF
[Unit]
Description=lazycomd - run and supervise long dev commands
After=network.target

[Service]
Type=simple
ExecStart=$BIN serve
ExecReload=$BIN reload
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
EOF
	systemctl --user daemon-reload
	systemctl --user enable --now lazycomd.service
}

case "$os" in
	darwin) install_launchd ;;
	linux) install_systemd ;;
esac

echo "installed $BIN"
if ! command -v lazycomd >/dev/null 2>&1; then
	echo "add $BIN_DIR to PATH if it is not already"
fi
echo "daemon is running and will start at login"
