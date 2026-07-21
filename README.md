# Android ADB Bridge v5.0.2

A lightweight, native Windows background service that controls Android TV devices via ADB to tune channels and stream live video for IPTV/Channels DVR integration.

---

## 1. Preparing Your Android TV Device (ADB Setup)

Before the bridge can automatically control your Android TV (e.g., Chromecast with Google TV, Nvidia Shield, Onn 4K, Fire TV), you must enable **Network Debugging**.

**Step 1: Enable Developer Options**
1. Navigate to **Settings** > **System** (or Device Preferences) > **About**.
2. Scroll down to the **Android TV OS build** (or just **Build**).
3. Click the OK/Select button on your remote **7 times** quickly.
4. You will see a toast message saying, *"You are now a developer!"*

**Step 2: Enable Network Debugging**
1. Go back to the main **Settings** menu.
2. Scroll down to **Developer Options** (usually located under System).
3. Toggle **USB Debugging** to **ON**.
4. *(If available on your specific device)* Toggle **Network Debugging** or **Wireless Debugging** to **ON**.

**Step 3: Get the Device IP Address**
1. Go to **Settings** > **Network & Internet**.
2. Select your active Wi-Fi or Ethernet connection.
3. Note the **IP Address** (e.g., `192.168.1.50`). You will need this in the Bridge UI.

*CRITICAL NOTE: The very first time the Bridge attempts to connect to your Android TV, a prompt will appear on your actual TV screen asking to "Allow USB debugging". Check "Always allow from this computer" and select OK. The bridge will not work until this is accepted.*

---

## 2. Installation and Launch

1. Run the `AndroidBridge_Setup_v5.0.0.exe` installer.
2. The installer will automatically add the necessary Windows Firewall rules and set the app to start silently when Windows boots.
3. Once installed, double-click the **Android ADB Bridge** shortcut on your Desktop or Start Menu. 
4. This will automatically open your default web browser to the dashboard (e.g., `http://192.168.1.X:8888/status`).

---

## 3. Configuring the Bridge

### Step 1: Add a Tuner (Device & Encoder)
In the Web UI, click **Add Device** under the Tuners section.
* **Name:** A friendly name (e.g., Living Room Shield).
* **Device IP:** The Android TV IP address you found earlier.
* **Encoder URL:** The raw HTTP stream URL from your LinkPi or hardware encoder.

### Step 2: Add a Provider (Streaming App)
Click **Add Provider** to define the apps that play your channels.
* **Provider Name:** e.g., YouTube TV
* **Internal ID:** e.g., `yt_tv`
* **App Intent:** The Android package name (e.g., `com.google.android.youtube.tvunplugged/com.google.android.apps.youtube.tvunplugged.activity.MainActivity`)
* **URL Template:** `https://tv.youtube.com/watch/{id}`

### Step 3: Add Channels
Click **Add Channel** to map specific broadcasts.
* **Channel Name:** e.g., ESPN
* **Unique ID:** e.g., `espn_1` (no spaces)
* **Provider:** Select the provider from the dropdown.
* **Deep Link ID:** The unique video ID for the stream.
* **Guide Station ID:** (Optional) Gracenote ID for EPG data in Channels DVR.

*Click the green **Save Changes to Disk** button whenever you finish making updates!*

---

## 4. Integration (Using the M3U)

To import your Android TV streams into a DVR or player like Channels DVR or VLC:
1. Click the **Copy M3U Link** button at the top of the Status page.
2. Paste this URL into your IPTV software's Custom Channel / M3U source.
3. The Bridge will automatically handle the tuning, concurrency locking, and video stream proxying natively!

## 5. Built-in Remote & Live Preview
* **Remote:** Click the "Open Remote" button to send standard D-pad and media playback commands directly to your Android devices from your PC or phone.
* **Live Preview:** Click the purple "Play" icon next to any channel in your list to verify the stream is working directly in your browser.

## 6. LinkPi Encoder Optimization Settings

Access your LinkPi web dashboard and verify the primary stream settings:

* **Video Codec & Profile:** Set the codec to **H.264** and select the **High** profile. This provides the highest compression efficiency and maximum visual fidelity for HD video, which modern playback hardware can decode flawlessly.
* **Bitrate Control:** Set to **CBR (Constant Bitrate)** rather than VBR. VBR causes sudden bitrate dips during static screens (like studio news backgrounds), which Channels DVR interprets as a frozen or dropped stream.
* **Bitrate:** **8,000–12,000 Kbps** for 1080p60.
* **Keyframe Interval (GOP):** Set this to **`1`** or **`2`**. In the LinkPi dashboard, the GOP unit is measured in **seconds**, not frames. A keyframe interval longer than 2 seconds causes slow tune times and playback buffering in Channels DVR.
* **Audio Format:** **AAC-LC**, 48 kHz, 192 Kbps. Ensure the audio sampling rate matches 48kHz, as 44.1kHz can drift over long recordings and cause micro-stutters.

