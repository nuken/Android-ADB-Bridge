# Android ADB Bridge v5.0.4 Win-Go

A lightweight background tool for Windows that connects your PC to your streaming sticks (like Chromecast, Nvidia Shield, or Onn 4K boxes). It automatically changes channels on your streaming apps and sends the live video straight to your DVR software.

---

## Table of Contents
- [1. Preparing Your Streaming Stick (Developer Connection)](#1-preparing-your-streaming-stick-developer-connection)
- [2. Installation and Launch](#2-installation-and-launch)
- [3. Setting Up Your Channels and Devices](#3-setting-up-your-channels-and-devices)
- [4. Connecting to Your DVR](#4-connecting-to-your-dvr)
- [5. Built-in Remote Control & Live Preview](#5-built-in-remote-control--live-preview)
- [6. Hardware Video Encoder Tips (For Best Picture & Speed)](#6-hardware-video-encoder-tips-for-best-picture--speed)
- [7. Using a Local USB Capture Card (New!)](#7-using-a-local-usb-capture-card-new)

---

## 1. Preparing Your Streaming Stick (Developer Connection)

Before this tool can control your streaming stick, you need to turn on a built-in safety setting called **Network Debugging**. Don't worry—it sounds complicated, but it just takes a few clicks with your remote.

**Step 1: Unlock Developer Settings**

1. Grab your TV remote and go to **Settings** > **System** (or Device Preferences) > **About**.
2. Scroll down until you see **Android TV OS build** (or just **Build**).
3. Click the **OK** button on that option **7 times** quickly.
4. A pop-up message will appear saying, *"You are now a developer!"*

**Step 2: Turn on Network Debugging**

1. Press the back button to return to the main **Settings** menu.
2. Scroll down and open your new **Developer Options** menu (usually found under System).
3. Turn **USB Debugging** to **ON**.
4. If you see an option for **Network Debugging** or **Wireless Debugging**, turn that **ON** as well.

**Step 3: Write Down Your TV's IP Address**

1. Go to **Settings** > **Network & Internet**.
2. Select your connected Wi-Fi or Ethernet network.
3. Look for the **IP Address** (it will look something like `192.168.1.50`). Write this down—you will need it later.

 **IMPORTANT FIRST-TIME STEP:** The very first time this tool tries to talk to your TV, a box will pop up on your TV screen asking to *"Allow USB debugging"*. Check the box that says **"Always allow from this computer"** and select **OK**. The tool will not work until you click OK on the TV screen.

---

## 2. Installation and Launch

1. Double-click the `AndroidBridge_Setup_v5.0.4.exe` file to run the installer.
2. The installer automatically handles safety settings (like Windows Firewall) and sets the app to start up quietly in the background whenever you turn on your PC.
3. Once installed, double-click the **Android ADB Bridge** shortcut on your Desktop or Start Menu.
4. This will automatically open your web browser to the app's control panel (usually `http://192.168.1.X:8888/status`).

---

## 3. Setting Up Your Channels and Devices

* **Name:** Give it a friendly name (e.g., Living Room Shield).
* **Device IP:** Type in the TV IP address you wrote down earlier.
* **Encoder URL:** The live video link coming out of your hardware video encoder box (like a LinkPi).

### Step 2: Add a Provider (Your Streaming App)

*:link:[Common Intent List](docs/intent.md)*

Click **Add Provider** to tell the system which app you are watching:

* **Provider Name:** e.g., YouTube TV
* **Internal ID:** e.g., `yt_tv`
* **Package Name:** The core system name of the app (e.g., `com.google.android.youtube.tvunplugged`)
* **Component (Activity):** The specific launch command (e.g., `com.google.android.apps.youtube.tvunplugged.activity.MainActivity`)
* **URL Template:** `https://tv.youtube.com/watch/{id}`
### Step 3: Add Channels

Click **Add Channel** to map out your favorite networks:

* **Channel Name:** e.g., ESPN
* **Unique ID:** e.g., `espn_1` (no spaces allowed)
* **Provider:** Choose your streaming app from the dropdown list.
* **Deep Link ID:** The specific web code or show ID for that channel.
* **Guide Station ID:** (Optional) The official TV guide ID so your DVR can download automatic show pictures and descriptions.

*Don't forget to click the green **Save Changes to Disk** button at the bottom when you are done adding channels!*

---

## 4. Connecting to Your DVR

To bring your streaming channels into your favorite TV guide software (like Channels DVR):

1. Click the **Copy M3U Link** button at the top of the Status page.
2. Paste that link directly into your DVR software as a **Custom Channel / M3U Source**.
3. The app will automatically handle changing the channels on your TV boxes whenever you hit play!

---

## 5. Built-in Remote Control & Live Preview

* **Virtual Remote:** Click the "Open Remote" button on the dashboard to control your streaming sticks right from your computer keyboard or phone.
* **Live Video Preview:** Click the purple "Play" icon next to any channel in your list to instantly test and watch the stream right inside your web browser.

---

## 6. Hardware Video Encoder Tips (For Best Picture & Speed)

If you are using a LinkPi or similar hardware encoder box, log into its web settings page and double-check these options to prevent lagging or stuttering:

* **Video Format:** Choose **H.264** and set the Profile to **High**. This gives you the cleanest, sharpest HD picture.
* **Bitrate Style:** Set this to **CBR (Constant Bitrate)** rather than variable. This keeps the video smooth and stops your DVR from thinking the channel is freezing.
* **Video Quality (Bitrate):** Set between **8,000 to 12,000 Kbps** for clear 1080p sports and shows.
* **Keyframe Speed (GOP):** Set this to **`1`** or **`2`** seconds. (Note: Make sure it's set to seconds, not frames). This ensures the video pops up instantly on your screen when you change the channel without endless buffering.
* **Audio Format:** Choose **AAC-LC**, **48 kHz**, at **192 Kbps** to keep the sound locked tightly in sync with the picture over long recordings.

---

## 7. Using a Local USB Capture Card (New!)

If you don't want to buy or configure a network encoder (like a LinkPi), you can now use a standard, inexpensive USB HDMI capture dongle plugged directly into your PC! The Android Bridge will automatically find the capture card, grab the video, and send it straight to your DVR.

**Step 1: The Hardware Setup**

1. Plug your Android TV stick directly into the HDMI port of your USB capture card.
2. Plug the USB capture card into an available USB port on your PC.
3. Power the Android TV stick using its normal wall plug.

**Step 2: Adding it to the Dashboard**

1. Open the Android ADB Bridge web dashboard.
2. Click **Add Device** under the Tuners section.
3. Enter a friendly name and the IP address of your Android stick.
4. Change the **Capture Type** dropdown to **Local USB Capture Card**.
5. The app will automatically search your PC for connected hardware. Simply select your capture card from the **Video** and **Audio** dropdown menus (it will usually be named something like `USB3 Video`, `USB Video`, or `FHD Capture`).
6. Click **Save**. The dashboard will display a green checkmark once the camera is ready to stream!

> ** Pro-Tip for Multiple Dongles:**
> A single 1080p video stream uses a lot of USB bandwidth. If you are plugging **two** capture cards into the same PC, try to spread them out. Plug one into the back of your computer (directly into the motherboard) and plug the second one into the front panel of your PC case. This prevents the USB ports from getting overloaded and guarantees smooth playback.
