#!/bin/bash

echo "Installing dependencies..."
sudo apt update
sudo apt install -y adb ffmpeg wget

echo "Downloading Android ADB Bridge..."
wget https://github.com/nuken/Android-ADB-Bridge/releases/download/v5.0.7-Linux/adb-bridge -O /tmp/adb-bridge

echo "Moving binary to system folder..."
sudo mv /tmp/adb-bridge /usr/local/bin/adb-bridge
sudo chmod +x /usr/local/bin/adb-bridge

echo "Creating systemd background service..."
# Notice we removed the -port flag here so main.go can auto-decide!
sudo tee /etc/systemd/system/adb-bridge.service > /dev/null <<EOF
[Unit]
Description=Android ADB Bridge Tuner
After=network-online.target
Wants=network-online.target

[Service]
User=$USER
Group=$USER
ExecStart=/usr/local/bin/adb-bridge
Restart=always
RestartSec=5
WorkingDirectory=$HOME
StandardOutput=syslog
StandardError=syslog
SyslogIdentifier=adb-bridge

[Install]
WantedBy=multi-user.target
EOF

echo "Starting the service..."
sudo systemctl daemon-reload
sudo systemctl enable adb-bridge
sudo systemctl start adb-bridge

echo "Waiting for app to initialize and find an open port..."
sleep 2

# Read the actual port the Go app saved into the JSON config file
CONFIG_FILE="$HOME/.castor/android_channels.json"
if [ -f "$CONFIG_FILE" ]; then
    # Grab the line with "port": and extract just the numbers
    ACTUAL_PORT=$(grep '"port":' "$CONFIG_FILE" | grep -o '[0-9]\+')
else
    # Fallback just in case
    ACTUAL_PORT="8888"
fi

# Automatically determine the server's local IP address
SERVER_IP=$(hostname -I | awk '{print $1}')

echo ""
echo "====================================================="
echo " Installation Complete!"
echo " Access your dashboard here: http://${SERVER_IP}:${ACTUAL_PORT}/status"
echo "====================================================="
