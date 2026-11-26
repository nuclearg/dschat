package com.dschat.android

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.graphics.Color
import android.graphics.Bitmap
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.view.WindowInsetsController
import android.webkit.ConsoleMessage
import android.webkit.JavascriptInterface
import android.webkit.PermissionRequest
import android.webkit.RenderProcessGoneDetail
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebChromeClient.FileChooserParams
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import android.window.OnBackInvokedCallback
import android.window.OnBackInvokedDispatcher

/**
 * Single-activity WebView shell. The Go engine lives in [EngineService]; this
 * activity only waits for [EngineService.url] and then loads it. It does not
 * own the engine, so rotation/recreation and backgrounding are harmless.
 */
class MainActivity : Activity() {

    private lateinit var webView: WebView
    private lateinit var overlay: View
    private lateinit var progress: ProgressBar
    private lateinit var statusText: TextView
    private lateinit var retryButton: Button

    private val handler = Handler(Looper.getMainLooper())
    private var polls = 0
    private var pageFailed = false
    private var lastLoadedUrl: String? = null

    private var filePathCallback: ValueCallback<Array<Uri>>? = null
    private var pendingPermissionRequest: PermissionRequest? = null
    private var pendingShareText: String? = null

    private val pollRunnable = Runnable { pollEngine() }

    private val backCallback: OnBackInvokedCallback? =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            OnBackInvokedCallback { handleBack() }
        } else {
            null
        }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        configureWindow()
        buildUi()
        configureWebView()
        updateSystemBarAppearance()
        registerBackCallback()
        requestNotificationPermission()
        handleShareIntent(intent)
        EngineService.start(this)
        showLoading(getString(R.string.loading))
        startPolling()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleShareIntent(intent)
    }

    override fun onResume() {
        super.onResume()
        updateSystemBarAppearance()
        if (::webView.isInitialized) webView.onResume()
    }

    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        updateSystemBarAppearance()
    }

    override fun onPause() {
        if (::webView.isInitialized) webView.onPause()
        super.onPause()
    }

    override fun onDestroy() {
        handler.removeCallbacks(pollRunnable)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && backCallback != null) {
            onBackInvokedDispatcher.unregisterOnBackInvokedCallback(backCallback)
        }
        filePathCallback?.onReceiveValue(null)
        filePathCallback = null
        pendingPermissionRequest?.deny()
        pendingPermissionRequest = null
        if (::webView.isInitialized) {
            (webView.parent as? ViewGroup)?.removeView(webView)
            webView.destroy()
        }
        // Backgrounding and recreation keep the engine alive; an explicit exit
        // (back) stops it so the foreground notification does not linger.
        if (isFinishing && !isChangingConfigurations) {
            EngineService.stop(this)
        }
        super.onDestroy()
    }

    // --------------------------------------------------------------- share

    /** Intent from Android's share sheet: DeepSeek shares text/plain links. */
    private fun handleShareIntent(intent: Intent?) {
        val shared = when (intent?.action) {
            Intent.ACTION_SEND -> intent.getStringExtra(Intent.EXTRA_TEXT)
            Intent.ACTION_VIEW -> intent.dataString
            else -> null
        }?.trim()
        if (shared.isNullOrEmpty()) {
            return
        }
        pendingShareText = shared
        dispatchPendingShare()
    }

    private fun dispatchPendingShare() {
        if (!::webView.isInitialized || lastLoadedUrl == null) {
            return
        }
        webView.evaluateJavascript(
            "window.dispatchEvent(new CustomEvent('dschat:share'))",
            null,
        )
    }

    private inner class ShareBridge {
        @JavascriptInterface
        fun takePendingShare(): String? {
            val text = pendingShareText
            pendingShareText = null
            return text
        }
    }

    // ---------------------------------------------------------------- engine

    private fun startPolling() {
        polls = 0
        handler.removeCallbacks(pollRunnable)
        handler.post(pollRunnable)
    }

    private fun pollEngine() {
        val engineUrl = EngineService.url
        if (engineUrl != null) {
            showContent()
            if (engineUrl != lastLoadedUrl || pageFailed) {
                lastLoadedUrl = engineUrl
                pageFailed = false
                webView.loadUrl(engineUrl)
            }
            return
        }

        val error = EngineService.lastError
        if (error != null) {
            Log.e(TAG, "engine start error: $error")
            showError(getString(R.string.error_engine, error))
            return
        }

        if (polls++ >= MAX_POLLS) {
            showError(getString(R.string.error_timeout))
            return
        }
        handler.postDelayed(pollRunnable, POLL_INTERVAL_MS)
    }

    private fun retry() {
        pageFailed = false
        showLoading(getString(R.string.loading))
        val engineUrl = EngineService.url
        if (engineUrl == null) {
            EngineService.retry(this)
            startPolling()
        } else {
            lastLoadedUrl = engineUrl
            webView.loadUrl(engineUrl)
        }
    }

    // ------------------------------------------------------------------- UI

    private fun configureWindow() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            window.setDecorFitsSystemWindows(false)
            window.statusBarColor = Color.TRANSPARENT
            window.navigationBarColor = Color.TRANSPARENT
        }
    }

    @Suppress("DEPRECATION")
    private fun updateSystemBarAppearance() {
        val night = (resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK) ==
            Configuration.UI_MODE_NIGHT_YES
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            val controller = window.insetsController ?: return
            val mask = WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS or
                WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS
            controller.setSystemBarsAppearance(if (night) 0 else mask, mask)
        } else {
            var flags = window.decorView.systemUiVisibility
            flags = if (night) {
                flags and View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR.inv()
            } else {
                flags or View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR
            }
            window.decorView.systemUiVisibility = flags
        }
    }

    private fun buildUi() {
        val root = FrameLayout(this)

        // targetSdk 37 draws edge-to-edge; pad the WebView/overlay by the
        // system bars and keyboard instead of letting controls slide under them.
        root.setOnApplyWindowInsetsListener { view, insets ->
            // Below API 30 the decor already fits the system bars; only the
            // edge-to-edge path needs explicit padding.
            if (Build.VERSION.SDK_INT < Build.VERSION_CODES.R) {
                return@setOnApplyWindowInsetsListener insets
            }
            val bars = insets.getInsets(
                WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout(),
            )
            val ime = insets.getInsets(WindowInsets.Type.ime()).bottom
            view.setPadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, ime))
            insets
        }

        webView = WebView(this)
        root.addView(
            webView,
            FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            ),
        )

        val overlayLayout = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            setPadding(dp(32), dp(32), dp(32), dp(32))
            setBackgroundColor(getColor(R.color.engine_bg))
        }

        progress = ProgressBar(this)
        overlayLayout.addView(progress)

        statusText = TextView(this).apply {
            setTextColor(getColor(R.color.engine_fg))
            textSize = 16f
            gravity = Gravity.CENTER
            setPadding(0, dp(16), 0, dp(16))
        }
        overlayLayout.addView(statusText)

        retryButton = Button(this).apply {
            text = getString(R.string.retry)
            visibility = View.GONE
            setOnClickListener { retry() }
        }
        overlayLayout.addView(retryButton)

        overlay = overlayLayout
        root.addView(
            overlay,
            FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            ),
        )
        setContentView(root)
        root.requestApplyInsets()
    }

    private fun showLoading(message: String) {
        statusText.text = message
        progress.visibility = View.VISIBLE
        retryButton.visibility = View.GONE
        overlay.visibility = View.VISIBLE
    }

    private fun showError(message: String) {
        statusText.text = message
        progress.visibility = View.GONE
        retryButton.visibility = View.VISIBLE
        overlay.visibility = View.VISIBLE
    }

    private fun showContent() {
        overlay.visibility = View.GONE
    }

    private fun dp(value: Int): Int = (value * resources.displayMetrics.density).toInt()

    // --------------------------------------------------------------- WebView

    @SuppressLint("SetJavaScriptEnabled")
    private fun configureWebView() {
        val settings = webView.settings
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = true
        settings.databaseEnabled = true
        settings.allowFileAccess = false
        settings.allowContentAccess = true
        settings.javaScriptCanOpenWindowsAutomatically = false
        settings.setSupportMultipleWindows(false)
        settings.loadWithOverviewMode = true
        settings.useWideViewPort = true
        settings.mediaPlaybackRequiresUserGesture = false
        settings.mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
        settings.userAgentString =
            "${settings.userAgentString} DSChatAndroid/${packageVersionName()}"

        webView.addJavascriptInterface(ShareBridge(), "DschatAndroid")
        webView.setBackgroundColor(getColor(R.color.engine_bg))
        if ((applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE) != 0) {
            WebView.setWebContentsDebuggingEnabled(true)
        }

        webView.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(
                view: WebView,
                request: WebResourceRequest,
            ): Boolean {
                val uri = request.url
                val host = uri.host
                if (host == "127.0.0.1" || host == "localhost") return false
                openExternally(uri)
                return true
            }

            override fun onPageStarted(view: WebView?, url: String?, favicon: Bitmap?) {
                pageFailed = false
            }

            override fun onPageFinished(view: WebView?, url: String?) {
                if (!pageFailed) {
                    showContent()
                    dispatchPendingShare()
                }
            }

            override fun onReceivedError(
                view: WebView?,
                request: WebResourceRequest?,
                error: WebResourceError?,
            ) {
                if (request?.isForMainFrame == true) {
                    pageFailed = true
                    Log.w(
                        TAG,
                        "main frame error ${error?.errorCode} ${error?.description} for ${request.url}",
                    )
                    showError(getString(R.string.error_page))
                }
            }

            override fun onRenderProcessGone(
                view: WebView,
                detail: RenderProcessGoneDetail,
            ): Boolean {
                Log.e(TAG, "WebView renderer gone, didCrash=${detail.didCrash()}")
                if (view === webView && !isFinishing) {
                    showError(getString(R.string.error_page))
                    // Recreating the activity rebuilds only the WebView; the
                    // foreground EngineService keeps chatsvr and in-flight turns.
                    handler.postDelayed({
                        if (!isFinishing) recreate()
                    }, 500)
                }
                return true
            }
        }

        webView.webChromeClient = object : WebChromeClient() {
            override fun onShowFileChooser(
                webView: WebView,
                filePathCallback: ValueCallback<Array<Uri>>,
                fileChooserParams: FileChooserParams,
            ): Boolean {
                this@MainActivity.filePathCallback?.onReceiveValue(null)
                this@MainActivity.filePathCallback = filePathCallback

                val intent = Intent(Intent.ACTION_GET_CONTENT).apply {
                    addCategory(Intent.CATEGORY_OPENABLE)
                    type = "*/*"
                    val mimeTypes = fileChooserParams.acceptTypes
                        ?.map { it.trim() }
                        ?.filter { it.contains("/") }
                        ?.takeIf { it.isNotEmpty() }
                    if (mimeTypes != null) {
                        putExtra(Intent.EXTRA_MIME_TYPES, mimeTypes.toTypedArray())
                    }
                    if (fileChooserParams.mode == FileChooserParams.MODE_OPEN_MULTIPLE) {
                        putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
                    }
                }
                return try {
                    startActivityForResult(intent, REQUEST_FILE_CHOOSER)
                    true
                } catch (e: ActivityNotFoundException) {
                    this@MainActivity.filePathCallback = null
                    filePathCallback.onReceiveValue(null)
                    false
                }
            }

            override fun onPermissionRequest(request: PermissionRequest) {
                if (!request.resources.contains(PermissionRequest.RESOURCE_AUDIO_CAPTURE)) {
                    request.deny()
                    return
                }
                if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) ==
                    PackageManager.PERMISSION_GRANTED
                ) {
                    request.grant(arrayOf(PermissionRequest.RESOURCE_AUDIO_CAPTURE))
                } else {
                    pendingPermissionRequest?.deny()
                    pendingPermissionRequest = request
                    requestPermissions(
                        arrayOf(Manifest.permission.RECORD_AUDIO),
                        REQUEST_MICROPHONE,
                    )
                }
            }

            override fun onPermissionRequestCanceled(request: PermissionRequest) {
                if (pendingPermissionRequest === request) pendingPermissionRequest = null
            }

            override fun onConsoleMessage(message: ConsoleMessage): Boolean {
                Log.d(TAG, "web: ${message.message()} (${message.sourceId()}:${message.lineNumber()})")
                return true
            }
        }
    }

    private fun openExternally(uri: Uri) {
        try {
            startActivity(Intent(Intent.ACTION_VIEW, uri))
        } catch (e: ActivityNotFoundException) {
            Log.w(TAG, "no activity can open $uri")
        }
    }

    private fun packageVersionName(): String = try {
        packageManager.getPackageInfo(packageName, 0).versionName ?: "0"
    } catch (e: PackageManager.NameNotFoundException) {
        "0"
    }

    // ------------------------------------------------------------- lifecycle

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode != REQUEST_FILE_CHOOSER) {
            super.onActivityResult(requestCode, resultCode, data)
            return
        }

        val callback = filePathCallback ?: return
        filePathCallback = null

        if (resultCode != RESULT_OK || data == null) {
            callback.onReceiveValue(null)
            return
        }

        val clip = data.clipData
        val uris: Array<Uri> = when {
            clip != null && clip.itemCount > 0 ->
                Array(clip.itemCount) { index -> clip.getItemAt(index).uri }
            data.data != null -> arrayOf(data.data!!)
            else -> emptyArray()
        }
        callback.onReceiveValue(if (uris.isEmpty()) null else uris)
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray,
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != REQUEST_MICROPHONE) return

        val pending = pendingPermissionRequest
        pendingPermissionRequest = null
        if (pending == null) return

        if (grantResults.isNotEmpty() && grantResults[0] == PackageManager.PERMISSION_GRANTED) {
            pending.grant(arrayOf(PermissionRequest.RESOURCE_AUDIO_CAPTURE))
        } else {
            pending.deny()
        }
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        handleBack()
    }

    private fun handleBack() {
        if (!::webView.isInitialized) {
            finish()
            return
        }
        // Let the React UI close a sidebar/settings modal before the native
        // shell pops history or exits. This is what makes an edge-swipe back
        // gesture close the visible surface instead of leaving the app.
        webView.evaluateJavascript(
            "(window.DschatAndroidBack && window.DschatAndroidBack()) === true",
        ) { handled ->
            if (handled != "true") {
                finishOrGoBack()
            }
        }
    }

    private fun finishOrGoBack() {
        val engineStarting = overlay.visibility == View.VISIBLE && EngineService.url == null
        if (!engineStarting && !pageFailed && webView.canGoBack()) {
            webView.goBack()
        } else {
            finish()
        }
    }

    @Suppress("DEPRECATION")
    private fun registerBackCallback() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && backCallback != null) {
            onBackInvokedDispatcher.registerOnBackInvokedCallback(
                OnBackInvokedDispatcher.PRIORITY_DEFAULT,
                backCallback,
            )
        }
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) !=
            PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(
                arrayOf(Manifest.permission.POST_NOTIFICATIONS),
                REQUEST_NOTIFICATIONS,
            )
        }
    }

    companion object {
        private const val TAG = "DSChat"
        private const val REQUEST_FILE_CHOOSER = 1001
        private const val REQUEST_MICROPHONE = 1002
        private const val REQUEST_NOTIFICATIONS = 1003
        private const val POLL_INTERVAL_MS = 100L
        private const val MAX_POLLS = 300 // ~30 seconds
    }
}
