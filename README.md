# Android ADB Bridge v5.1.0 Win-Go

A lightweight background tool for Windows that connects your PC to your streaming sticks (like Chromecast, Nvidia Shield, or Onn 4K boxes). It automatically changes channels on your streaming apps and sends the live video straight to your DVR software.

---

### Important Notice: Windows SmartScreen & Security Exclusions

If you are installing **Android ADB Bridge** on Windows, you will likely encounter a **Windows Defender SmartScreen** warning, and you should know that the installer automatically configures a security exclusion for the app.

**Please be assured that the application is completely safe.** Here is exactly what is happening under the hood and why:

#### 1. The Windows SmartScreen Popup

When you launch the installer, you may see a blue screen stating *"Windows protected your PC"* due to an unrecognized app.

* **Why this happens:** This is a standard warning for software that is not signed with a commercial Code Signing Certificate. Because this is a free, open-source community tool, it does not have the paid digital signature that Microsoft requires to automatically bypass this screen.
* **What to do:** Simply click **More info**, and then click **Run anyway** to launch the installer.

#### 2. Automated Windows Defender Exclusion

During installation, the setup wizard will silently run a script to add the application's installation folder (typically `C:\Program Files (x86)\AndroidBridge`) to your Windows Defender exclusion list.

* **Why this is necessary:** Android ADB Bridge is a powerful proxy server written in Go. To ensure your channel tuning is fast and seamless, the application constantly launches silent background tasks (like `adb.exe` and `ffmpeg.exe`) and listens on local network ports. Aggressive AI and "Machine Learning" antivirus behavior-based scanning frequently mistake this legitimate background automation for a false positive (often flagging it as a generic Trojan).
* **What it does:** By automatically whitelisting the application's specific folder during setup, the installer ensures that your antivirus does not falsely quarantine the executable, allowing your video streams to run smoothly and uninterrupted. The installer will cleanly remove this exception if you ever uninstall the application.



*If you ever want to verify exactly what the application or the installer is doing, the complete source code is fully open-source and available for review in this repository.*

---

## Table of Contents
- [1. Preparing Your Streaming Stick (Developer Connection)](#1-preparing-your-streaming-stick-developer-connection)
- [2. Installation and Launch](#2-installation-and-launch)
- [3. Setting Up Your Channels and Devices](#3-setting-up-your-channels-and-devices)
- [4. Connecting to Your DVR](#4-connecting-to-your-dvr)
- [5. Built-in Remote Control & Live Preview](#5-built-in-remote-control--live-preview)
- [6. Hardware Video Encoder Tips (For Best Picture & Speed)](#6-hardware-video-encoder-tips-for-best-picture--speed)
- [7. Using a Local USB Capture Card (New!)](#7-using-a-local-usb-capture-card-new)
- [8. Simulated Keypress Tuning (Macros)](#8-simulated-keypress-tuning-macros)
- [9. Fire OS / Fire TV Support](#9-fire-os--fire-tv-support)
- [10. Cloud & Local Channel Packs](#10-cloud--local-channel-packs)
- [11. Direct USB ADB Connections (Wired Mode)](#11-direct-usb-adb-connections-wired-mode)

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

1. Double-click the `AndroidBridge_Setup_v5.1.0.exe` file to run the installer.
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

---

## 8. Simulated Keypress Tuning (Macros)

*:link:[Keypress Tuning Guide](docs/macros.md)*

Not all streaming apps support "Deep Links." If an app requires you to manually navigate its menus to change the channel, the Android ADB Bridge can do it for you using **Simulated Keypress Tuning (Macros)**.

This system allows you to build a sequence of remote control button presses (like `UP`, `DOWN`, `ENTER`, or standard number keys like `1`, `0`, `4`) that the bridge will automatically execute every time a channel is requested.

**The Macro Tuning Sequence:**
When a channel without a Deep Link is tuned, the bridge executes commands in this exact order:

1. **Pre-Tune Macro:** (Set in the *Provider* menu). Useful for returning the app to a known "Home" state before navigating (e.g., `HOME, WAIT:1000`).
2. **App Launch:** The app is brought to the foreground.
3. **Splash Delay:** A required pause to let the app finish loading.
4. **Channel Tuning Macro:** (Set in the *Channel* menu). The sequence to actually select the channel (e.g., `DOWN:3, ENTER` or `1, 0, 4, ENTER`).
5. **Post-Tune Macro:** (Set in the *Provider* menu). Any final cleanup commands, like dismissing an on-screen guide (e.g., `WAIT:2000, ENTER`).

**How to Build Macros:**
Open the **Add Channel** or **Edit Provider** modal and use the built-in Macro Toolbar. Click the directional icons or use the **Numpad** button to generate a clean, perfectly formatted comma-separated sequence.

---

## 9. Fire OS / Fire TV Support

You can now mix standard Android TV devices (like the Onn 4K) and Amazon Fire TV Sticks in the same tuner pool!

1. When adding or editing a device in the **Tuners** table, set the **Device Platform** to `Amazon Fire TV / Fire OS`.
2. When setting up your **Providers** (like YouTube TV or Prime Video), expand the **Fire TV Overrides** section.
3. Enter the specific `Fire OS Package Name` and `Fire OS Activity Component`.

When a tune is requested, the bridge automatically checks the hardware OS and seamlessly routes the command to the correct Fire OS intent, falling back to the standard Android TV intent if no override is provided.

---

## 10. Cloud & Local Channel Packs

*:link:[Channel Packs Guide](docs/packs.md)*

Setting up hundreds of channels manually can be tedious. Version 5.1.0 introduces a dual-import system to let you download entire channel lineups in seconds.

* **Import Channel Pack (Blue Button):** Loads packs designed for standard **Deep Link** URL tuning.
* **Import Macro Pack (Pink Button):** Loads packs specifically mapped out for **Simulated Keypress** tuning sequences.

For both options, you can choose to grab a pre-configured list directly from the **Cloud Repository** (hosted on GitHub), or you can select **Local PC File** to upload your own custom `.json` configurations.

---

## 11. Direct USB ADB Connections (Wired Mode)

If your streaming stick frequently drops its Wireless ADB connection or resets its debug port upon rebooting (a known quirk with Google TV / Chromecast devices), you can bypass the network entirely by hardwiring it directly to your PC over USB.

**Requirements:**

* **Powered USB Hub:** Must supply at least **5V / 1.5A (7.5W)** per port to adequately power the Google TV and prevent boot-loops. *(Standard PC USB-A ports only output 4.5W and will fail to power the device).*
* **Standard USB Data Cable:** Connects the Google TV to the powered USB hub / PC.
* **CRITICAL:** Do **NOT** use a USB OTG Y-Cable for PC connectivity. OTG hardware forces the Google TV into "Host Mode," which prevents Windows and ADB from detecting the device.

**How to Configure:**

1. Connect your Google TV to a **Powered USB Hub** that is plugged into your PC using a standard USB data cable.
2. Open the **Android ADB Bridge** web dashboard.
3. Click **Add Device** (or edit an existing Tuner).
4. Change the **ADB Connection Route** dropdown from `Network (WiFi / LAN)` to `Direct USB (Data Cable)`.
5. Click the green **Auto-Detect** button next to the **USB Device Serial** field.
6. Select your device's hardware serial number from the newly populated dropdown list and click **Save / Apply**.

The bridge will now bypass IP-based connection commands and route all tuning macros directly over the physical USB cable, making the setup completely immune to network drops, router restarts, or wireless ADB port resets.
