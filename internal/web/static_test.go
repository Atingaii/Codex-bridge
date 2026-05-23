package web

import (
	"strings"
	"testing"
)

func TestStaticUIContracts(t *testing.T) {
	indexBytes, err := StaticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	cssBytes, err := StaticFS.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	jsBytes, err := StaticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	index := string(indexBytes)
	css := string(cssBytes)
	js := string(jsBytes)

	requiredHTMLIDs := []string{
		"loginView", "loginForm", "loginLangBtn", "chatView", "sidebar", "openNavBtn", "closeNavBtn",
		"newChatBtn", "sessionList", "messages", "promptForm", "prompt", "agentList",
		"langBtn", "collapseSidebarBtn", "runtimeSession", "runtimeRunner", "runtimeThread",
		"metricAgentValue", "metricSessionValue", "metricRunnerValue",
		"sessionSearch", "settingsOverlay", "settingsBtn", "themeLightBtn", "themeDarkBtn",
	}
	for _, id := range requiredHTMLIDs {
		if !strings.Contains(index, `id="`+id+`"`) {
			t.Fatalf("index.html missing id %q", id)
		}
	}
	requiredJSIDs := []string{
		"loginView", "loginForm", "loginLangBtn", "chatView", "openNavBtn", "closeNavBtn",
		"newChatBtn", "sessionList", "messages", "promptForm", "prompt", "agentList",
		"langBtn", "collapseSidebarBtn", "runtimeSession", "runtimeRunner", "runtimeThread",
		"metricAgentValue", "metricSessionValue", "metricRunnerValue",
		"sessionSearch", "settingsOverlay", "settingsBtn", "themeLightBtn", "themeDarkBtn",
	}
	for _, id := range requiredJSIDs {
		if !strings.Contains(js, `"`+id+`"`) {
			t.Fatalf("app.js does not reference id %q", id)
		}
	}

	requiredCSS := []string{
		"@media (max-width: 760px)",
		".workspace.nav-open .sidebar",
		"min-height: 100dvh",
		"env(safe-area-inset-bottom)",
		"grid-template-columns: 292px minmax(0, 1fr)",
		"overscroll-behavior: contain",
		"scrollbar-width: thin",
		"::-webkit-scrollbar-thumb",
		".workspace.sidebar-collapsed",
		"grid-template-columns: 0 minmax(0, 1fr)",
		"html {",
		"overflow: hidden",
		".status-strip",
		".status-pill",
		".chat-card",
		".bubble pre",
		".copy-button",
		".code-frame",
		".code-toolbar",
		".tool-event",
		"width: min(860px, calc(100% - 36px))",
		".runtime-banner",
		".settings-modal",
		".session-actions",
	}
	for _, needle := range requiredCSS {
		if !strings.Contains(css, needle) {
			t.Fatalf("style.css missing responsive contract %q", needle)
		}
	}

	requiredI18N := []string{
		"normalizeLanguage(localStorage.getItem(\"codexBridge.lang\"))",
		"langSwitch: \"中文\"",
		"langSwitch: \"EN\"",
		"新会话",
		"反向隧道",
		"Enter 发送，Shift+Enter 换行",
		"event.key === \"Enter\" && !event.shiftKey && !event.isComposing",
		"metricRunnerHint",
		"markdownToHTML",
		"renderMarkdownInto",
		"createCopyButton",
		"copyTextToClipboard",
		"复制代码",
		"HEARTBEAT_MS",
		"ensureChatConnection",
		"sendHeartbeat",
		"setAssistantContent",
		"visibilitychange",
		"scheduleReconnect",
		"appendToolEvent",
		"activeRun",
		"canceling",
		"runActive",
		"window.crypto?.getRandomValues",
		"currentStatus === \"canceling\"",
		"restoreLinks",
		"codexBridge.sidebarCollapsed",
		"toggleSidebarCollapsed",
		"codexBridge.theme",
		"serviceWorker",
		"/app.js?v=20260523-saas1",
		"/style.css?v=20260523-saas1",
		"/manifest.webmanifest",
		"/icon.svg",
	}
	for _, needle := range requiredI18N[:30] {
		if !strings.Contains(js, needle) {
			t.Fatalf("app.js missing i18n contract %q", needle)
		}
	}
	for _, needle := range requiredI18N[30:] {
		if !strings.Contains(index, needle) {
			t.Fatalf("index.html missing i18n contract %q", needle)
		}
	}

	requiredStaticFiles := []string{
		"static/icon.svg",
		"static/manifest.webmanifest",
		"static/sw.js",
	}
	for _, name := range requiredStaticFiles {
		if _, err := StaticFS.ReadFile(name); err != nil {
			t.Fatalf("missing embedded static file %q: %v", name, err)
		}
	}
}
