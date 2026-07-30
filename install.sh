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
   
   echo "Installation Complete! Go to http://YOUR_SERVER_IP:8888/status"