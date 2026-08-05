
### 1. Channel Pack Formatting Guide (`docs/channel_pack_formatting.md`)


# Channel & Macro Pack Formatting Guide

The Android ADB Bridge supports importing bulk channel lineups via standard `.json` files. You can load these packs locally from your PC or host them on a cloud repository (like GitHub) for easy sharing.

There are two types of packs you can create:
1. **Deep Link Packs:** Designed for standard URL template tuning.
2. **Macro Packs:** Designed for Simulated Keypress Tuning (for apps without deep links).

Both types of packs use the exact same underlying JSON structure.

---

## 1. The Pack JSON Structure

A channel pack is a single JSON object containing two arrays: `providers` and `channels`. When a user imports a pack, the system will seamlessly merge these arrays into their existing configuration.

### Example Pack
```json
{
  "providers": [
    {
      "id": "spectrum",
      "name": "Spectrum TV",
      "package_name": "com.spectrum.tv",
      "component": "com.spectrum.tv.MainActivity",
      "fire_package_name": "",
      "fire_component": "",
      "url_template": "",
      "pre_tune_macro": "HOME, WAIT:1000",
      "post_tune_macro": "WAIT:2000, ENTER",
      "splash_delay_ms": 3000
    }
  ],
  "channels": [
    {
      "name": "ESPN",
      "id": "spectrum_espn",
      "provider_id": "spectrum",
      "deep_link_content_id": "",
      "tuning_macro": "1, 0, 4, ENTER",
      "tvc_guide_stationid": "32375"
    }
  ]
}

```

---

## 2. Field Definitions

### Provider Object

| Field | Type | Description |
| --- | --- | --- |
| `id` | String | Unique internal identifier for the provider (e.g., `yt_tv`). |
| `name` | String | Friendly display name (e.g., `YouTube TV`). |
| `package_name` | String | Android TV application package (e.g., `com.google.android.youtube.tvunplugged`). |
| `component` | String | Android TV main activity component. |
| `fire_package_name` | String | *(Optional)* Override package for Amazon Fire OS devices. |
| `fire_component` | String | *(Optional)* Override component for Amazon Fire OS devices. |
| `url_template` | String | *(Optional)* Deep link format string containing `{id}`. |
| `splash_delay_ms` | Integer | Milliseconds to pause (and mute audio) while the app loads. |
| `pre_tune_macro` | String | *(Optional)* ADB keystrokes to run *before* app launch. |
| `post_tune_macro` | String | *(Optional)* ADB keystrokes to run *after* tuning completes. |

### Channel Object

| Field | Type | Description |
| --- | --- | --- |
| `id` | String | Unique internal identifier for the channel (e.g., `espn_1`). |
| `name` | String | Friendly display name (e.g., `ESPN`). |
| `provider_id` | String | Must match the `id` of an existing Provider. |
| `deep_link_content_id` | String | *(Optional)* The specific video ID injected into the Provider's URL template. |
| `tuning_macro` | String | *(Optional)* ADB keystrokes used to select the channel inside the app. |
| `tvc_guide_stationid` | String | *(Optional)* Gracenote station ID for DVR guide mapping. |

---

## 3. Cloud Repository Index (`index.json`)

If you want to host packs on the cloud, you must provide an `index.json` file in your repository. The Android ADB Bridge reads this index to populate the dropdown menu in the UI.

The index must be a JSON array containing the pack details:

```json
[
  {
    "name": "Spectrum TV - Standard Lineup (Numpad)",
    "url": "[https://raw.githubusercontent.com/username/repo/main/packs/spectrum.json](https://raw.githubusercontent.com/username/repo/main/packs/spectrum.json)",
    "channel_count": 145
  },
  {
    "name": "YouTube TV - Base Lineup (Deep Links)",
    "url": "[https://raw.githubusercontent.com/username/repo/main/packs/yttv.json](https://raw.githubusercontent.com/username/repo/main/packs/yttv.json)",
    "channel_count": 82
  }
]

```
