# **Android ADB Bridge (Linux Tuner)**

A lightweight, Go-based headless server application that turns your Linux machine into a dedicated hardware-encoding appliance.

Android ADB Bridge uses Android Debug Bridge (ADB) to seamlessly control Android TV devices (like ONN 4K boxes, Chromecast, or Nvidia Shield) and captures their video output using local USB HDMI capture cards or network encoders (like LinkPi). It then processes the video using hardware-accelerated FFmpeg (Intel QuickSync or AMD VAAPI) and serves it as a dynamic M3U playlist, perfect for importing into **Channels DVR**.

## **Features**

* **Direct Hardware Encoding:** Bypass browsers and DRM limits. Leverage your CPU's integrated graphics (Intel QSV / AMD VAAPI) for highly efficient H.264/H.265 stream encoding.  
* **LinkPi-Style Controls:** Fine-tune video bitrates, audio bitrates, deinterlacing, audio sync delays, and hardware presets directly from the web UI.  
* **Native Channels DVR Integration:** Automatically injects tvc-guide-stationid tags into the generated M3U playlist for instant, zero-configuration EPG guide data.  
* **Headless & Persistent:** Runs as a detached systemd background service on Ubuntu/Linux.  
* **Zero Dependencies for the App:** The web dashboard and tuning logic are compiled into a single Go binary. (Only adb and ffmpeg are required on the host system).

## **Installation**

This project is designed to run on a Linux environment (e.g., Ubuntu Server 26.04). We have provided an install script that automatically installs the required dependencies (ADB and FFmpeg), downloads the latest binary, and sets it up as a background service.

1. Download the installation script to your Linux server:
   ``` 
   wget https://raw.githubusercontent.com/nuken/Android-ADB-Bridge/linux/install.sh
   ```

*(Note: Update the URL above if your repository structure is different).*

2. Make the script executable:
   ```
   chmod \+x install.sh
   ```
4. Run the installer:
   ```
   ./install.sh
   ```

Once the script finishes, the service will start automatically in the background.

## **How to Use**

### **1\. Access the Dashboard**

Open a web browser on any computer on your network and navigate to:

http://\<YOUR\_SERVER\_IP\>:8888/status

### **2\. Configure Your Setup**

The Web UI is divided into three main sections:

1. **Tuners (Devices):** Click **Add Device** to configure an Android TV box. You will need its local IP address. Choose whether the video is captured via a "Local USB Capture Card" (which allows you to select /dev/video and ALSA audio sources) or a "Network Encoder". *Make sure USB Debugging is enabled on the Android TV device\!*  
2. **Providers:** Click **Add Provider** to define a streaming app (e.g., YouTube TV, Hulu). You will need the app's Android Package Name and Component Activity.  
3. **Channels:** Click **Add Channel** to map specific streams. Here you will define the deep link ID for the content and the **Guide Station ID** (Gracenote ID).

### **3\. Connect to Channels DVR**

Once your channels are configured:

1. Click the **Copy M3U Link** button at the top of the ADB Bridge dashboard.  
2. Open your Channels DVR Server web interface.  
3. Navigate to **Sources** \> **Add Custom Channel** \> **M3U Playlist**.  
4. Set the stream format to **MPEG-TS** and paste the M3U URL.

Channels DVR will import the streams and automatically fetch 14-day guide data, logos, and descriptions based on the Station IDs you provided\!

## **Configuration Backup**

You can back up your entire configuration (Tuners, Providers, and Channels) by clicking the **Backup Config** button in the UI. If you ever move to a new server, simply click **Restore Config** and upload your saved .json file.
