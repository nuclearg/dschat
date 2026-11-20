# Android shell

Kotlin + Gradle WebView shell for DSChat. The Go engine is compiled into an
AAR with `gomobile` and hosted by a foreground service; the activity only
waits for the loopback URL and displays it in a `WebView`.

| Item | Value |
| --- | --- |
| Package / applicationId | `com.dschat.android` |
| Version | 0.1.3 (versionCode 4) |
| Launcher activity | `com.dschat.android.MainActivity` |
| Engine service | `com.dschat.android.EngineService` (`specialUse` foreground type) |
| minSdk / compileSdk / targetSdk | 26 / 37 / 37 |
| ABIs | `armeabi-v7a`, `arm64-v8a`, `x86`, `x86_64` (from gomobile) |
| UI stack | plain `android.app.Activity` + `WebView`; no AndroidX |

## Architecture

- `app/mobile/mobile.go` boots one in-process `chatsvr` instance and returns
  the authenticated URL (`http://127.0.0.1:<port>/auth?t=<token>`).
- `EngineService` calls `Mobile.start(filesDir.absolutePath)` on a worker
  thread, publishes the URL in a static holder, and runs as a foreground
  service so backgrounding or activity recreation does not kill in-flight
  chats. On API 34+ it calls the three-argument `startForeground` with
  `FOREGROUND_SERVICE_TYPE_SPECIAL_USE`; the manifest declares `specialUse`
  plus the `PROPERTY_SPECIAL_USE_FGS_SUBTYPE` property.
- `MainActivity` starts the service, polls the static URL (100 ms interval,
  30 s timeout), then loads it. While waiting, or after an error, it shows a
  dark loading/error overlay with a Retry button. `Retry` clears the previous
  start error and asks the service to try again.
- The engine survives Home/background, rotation (handled via `configChanges`),
  and forced activity recreation. When there is no WebView history to go back
  to, Back exits the app and stops the engine; swiping the task away does the
  same via `onTaskRemoved`.
- WebView: JavaScript and DOM storage are enabled; `WebChromeClient`
  implements `onShowFileChooser` with `ACTION_GET_CONTENT` (multi-select when
  the page asks for it), and `onPermissionRequest` grants
  `RESOURCE_AUDIO_CAPTURE` after requesting `RECORD_AUDIO`. Back first walks
  the WebView history (also via `OnBackInvokedCallback` on API 33+), then
  finishes the activity. External links open the system browser.
- Cleartext HTTP is limited to `127.0.0.1`/`localhost` by
  `res/xml/network_security_config.xml`; everything else remains TLS-only.
- The web UI is the same official demo bundle embedded in chatsvr, so Android
  automatically reuses the desktop features: session ownership/status icons,
  blocked/fragment badges, DeepSeek ZIP import, local delete, sessionsvr
  password login/switch/logout, Chinese/English UI and `X-Language`.
- Native WebView shell hardening:
  - targetSdk 37 edge-to-edge padding for system bars, display cutout, and IME;
  - light/dark system-bar icon appearance follows the system night mode;
  - Chinese native loading/error/notification strings via `values-zh`;
  - WebView renderer crash recovery recreates only the WebView, keeping the
    foreground engine and in-flight turns alive;
  - file chooser forwards WebView MIME type filters while keeping `*/*` for
    DeepSeek ZIP import.
- Downloads, camera capture, and release signing are intentionally not
  implemented.

## Toolchain used

These are the exact versions this project was built and tested with:

| Tool | Version |
| --- | --- |
| Go | 1.27.4 (darwin/arm64) |
| gomobile / gobind | `golang.org/x/mobile v0.0.0-20261117202452-47699113848a` |
| Android SDK platform | `platforms;android-37.0` (API 37) |
| Android build-tools | 36.0.0 |
| Android NDK | 27.2.12479018 (r27c) |
| JDK | OpenJDK 21.0.12 (AGP requires 17+) |
| Gradle | 9.9.0 (wrapper) |
| Android Gradle Plugin | 9.5.1 |

AGP 9.x has built-in Kotlin support, so the separate
`org.jetbrains.kotlin.android` plugin is not applied; AGP compiles the `.kt`
files in `src/main/java` itself. This also avoids AGP/KGP version skew.

## Build

All commands run from the repository root unless noted. `gomobile bind` reads
the embedded `app/core/web/dist`, so run it after any web rebuild.

For the common path there is a one-command build (it also writes
`local.properties` and copies both artifacts into `dist/android/`):

```sh
make android
# or directly:
./scripts/build-android.sh
```

The script auto-detects the newest NDK under `$ANDROID_HOME/ndk`, requires
`gomobile`/`gobind` on `PATH`, and runs the same two commands as below.

Manual steps:

```sh
export ANDROID_HOME="$HOME/Library/Android/sdk"
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/27.2.12479018"
export PATH="$HOME/go/bin:$PATH"

# Optional: only needed once per gomobile installation.
gomobile init

# 1) Go engine -> AAR (also writes chatsvr-sources.jar next to it).
mkdir -p app/android/app/libs
gomobile bind -target=android -androidapi 26 \
  -o app/android/app/libs/chatsvr.aar ./app/mobile

# 2) Release APK. local.properties is gitignored and machine-specific.
export ANDROID_KEYSTORE_PATH="$HOME/.android-keys/dschat-release.jks"
export ANDROID_KEYSTORE_PASSWORD=...
export ANDROID_KEY_ALIAS=dschat
export ANDROID_KEY_PASSWORD=...
cd app/android
printf 'sdk.dir=%s\n' "$ANDROID_HOME" > local.properties
./gradlew :app:assembleRelease

# 3) Collect deliverables.
cd ../..
mkdir -p dist/android
cp app/android/app/build/outputs/apk/release/app-release.apk dist/android/dschat-android-release.apk
cp app/android/app/libs/chatsvr.aar dist/android/chatsvr.aar
```

A clean build (`./gradlew clean :app:assembleDebug`) is also supported; the
Gradle wrapper downloads Gradle 9.9.0 if it is not already cached.

`go.mod` records the gobind tool directive
(`tool golang.org/x/mobile/cmd/gobind`). Keep `$HOME/go/bin` on `PATH`
regardless, because `gomobile bind` invokes the `gobind` binary.

## Install and run

```sh
adb install -r dist/android/dschat-debug.apk
adb shell am start -n com.dschat.android/.MainActivity
adb logcat -s DSChatEngine GoLog
```

The first launch asks for notification permission (needed for the foreground
service notification on Android 13+); granting it is optional but recommended.
The app has no API key initially; in this build the web UI opens its settings
sheet on first run.

## Verify the artifacts

```sh
SDK="$HOME/Library/Android/sdk"
"$SDK/build-tools/36.0.0/aapt2" dump badging dist/android/dschat-debug.apk | head
"$SDK/build-tools/36.0.0/apksigner" verify --verbose --print-certs dist/android/dschat-debug.apk
unzip -l dist/android/chatsvr.aar            # classes.jar + 4 libgojni.so
unzip -p dist/android/chatsvr.aar classes.jar > /tmp/classes.jar
javap -classpath /tmp/classes.jar mobile.Mobile
```

Expected package name `com.dschat.android`, launchable activity
`com.dschat.android.MainActivity`, and debug signing certificate
`CN=Android Debug`.

## Manual test evidence (API 37 emulator)

- `GoLog: dschat listening on http://127.0.0.1:5217` then
  `DSChatEngine: engine ready`.
- `dumpsys activity services com.dschat.android` shows
  `isForeground=true ... types=0x40000000` (special-use FGS) and the
  `dschat-engine` notification channel.
- Home/background then relaunch, forced activity recreation
  (`always_finish_activities=1`), and rotation all reused the same process
  PID and did not log another engine start; the WebView rendered the React UI
  each time.
- Back stopped the service (`service destroyed; stopping engine`) and a
  subsequent launch started a fresh engine.
- Rotating to landscape reflowed the web UI; tapping the API-key field showed
  the IME and `adjustResize` kept the field visible.

## Known limitations

- Debug-signed only; `release` is unminified and unsigned.
- The AAR (~46 MiB) and APK (~113 MiB) bundle all four ABIs; the native
  libraries are stored uncompressed (`extractNativeLibs=false`). Use ABI
  splits or an app bundle to shrink it.
- `chatsvr` listens on the fixed `127.0.0.1:5217`; if that port is busy the
  engine start fails and the shell shows the error. The URL is resolved by
  `Mobile.start`, so the WebView does not hardcode it.
- External `http(s)` links open the browser instead of an in-app tab.
- File selection uses the system document picker (`ACTION_GET_CONTENT`); camera
  capture is not offered. Only `RESOURCE_AUDIO_CAPTURE` is granted to pages;
  other WebView permission resources are denied.
- Android 13+ notification denial hides the FGS notification but does not
  stop the service.
- The emulator used a 4 KB page size. `zipalign -P 16` passes, but the
  `libgojni.so` produced by this gomobile/Go build has 4 KB-aligned `PT_LOAD`
  segments (`p_align=0x1000`); 16 KB-page devices were not validated and may
  reject the native library. A 16 KB-aware Go/gomobile build would be needed
  before shipping to those devices.
- No instrumentation tests; verification was manual on the emulator plus
  `go test ./app/mobile` for the façade lifecycle (not committed).
- `local.properties`, the generated `app/libs/` (AAR + sources jar), and
  Gradle build directories are gitignored; they are produced by the commands
  above.
