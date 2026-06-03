var recoveryKey = "codexBridge.legacyBundleRecovery.index-BWIkJOjq.v1";

function legacyAttempts() {
  try {
    return Number(sessionStorage.getItem(recoveryKey) || "0");
  } catch (_) {
    return 0;
  }
}

function setLegacyAttempts(value) {
  try {
    sessionStorage.setItem(recoveryKey, String(value));
  } catch (_) {
    // Session storage can be unavailable in restricted browsers.
  }
}

function rootHasContent() {
  var root = document.getElementById("root");
  return Boolean(root && root.childElementCount > 0);
}

function renderFallback(reason) {
  var root = document.getElementById("root");
  if (!root || rootHasContent()) return;
  var message = document.createElement("div");
  message.style.cssText = "box-sizing:border-box;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px;font:14px/1.5 system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;color:#111827;background:#f9fafb;";
  message.innerHTML = "<div style=\"max-width:520px\"><h1 style=\"margin:0 0 8px;font-size:18px\">Codex Bridge UI needs a refresh</h1><p style=\"margin:0;color:#4b5563\">The browser is still loading an old application bundle. Hard refresh this page or open it with a cache-busting query parameter. Reason: " + escapeHTML(reason || "legacy bundle") + ".</p></div>";
  root.appendChild(message);
}

function escapeHTML(value) {
  return String(value).replace(/[&<>\"']/g, function (char) {
    return {
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#39;"
    }[char];
  });
}

function clearBrowserState() {
  var tasks = [];
  if ("serviceWorker" in navigator && navigator.serviceWorker.getRegistrations) {
    tasks.push(
      navigator.serviceWorker.getRegistrations().then(function (registrations) {
        return Promise.all(registrations.map(function (registration) {
          return registration.unregister();
        }));
      })
    );
  }
  if ("caches" in window && window.caches.keys) {
    tasks.push(
      window.caches.keys().then(function (names) {
        return Promise.all(names.map(function (name) {
          return window.caches.delete(name);
        }));
      })
    );
  }
  return Promise.all(tasks).catch(function () {});
}

function currentModuleSource(html) {
  var match = html.match(/<script[^>]+type=["']module["'][^>]+src=["']([^"']+)["']/i);
  return match && match[1] ? match[1] : "";
}

function currentStylesheetSources(html) {
  var sources = [];
  var matcher = /<link[^>]+rel=["']stylesheet["'][^>]+href=["']([^"']+)["'][^>]*>/gi;
  var match;
  while ((match = matcher.exec(html)) !== null) {
    if (match[1]) sources.push(match[1]);
  }
  return sources;
}

function loadStylesheet(src) {
  if (!src) return;
  var absolute = new URL(src, window.location.origin).href;
  var existing = Array.prototype.some.call(document.querySelectorAll("link[rel='stylesheet']"), function (link) {
    return link.href === absolute;
  });
  if (existing) return;
  var link = document.createElement("link");
  link.rel = "stylesheet";
  link.href = absolute;
  document.head.appendChild(link);
}

function reloadWithCacheBust(reason) {
  var attempts = legacyAttempts();
  if (attempts >= 1) {
    renderFallback(reason);
    return;
  }
  setLegacyAttempts(attempts + 1);
  clearBrowserState().finally(function () {
    var url = new URL(window.location.href);
    url.searchParams.set("_cb_legacy_bundle", String(Date.now()));
    window.location.replace(url.href);
  });
}

fetch("/?_cb_legacy_bundle=" + Date.now(), { cache: "no-store" })
  .then(function (response) {
    if (!response.ok) throw new Error("failed to load current UI entry");
    return response.text();
  })
  .then(function (html) {
    var src = currentModuleSource(html);
    if (!src || src.indexOf("index-BWIkJOjq.js") >= 0) {
      reloadWithCacheBust("current entry is still cached");
      return;
    }
    currentStylesheetSources(html).forEach(loadStylesheet);
    var moduleURL = new URL(src, window.location.origin);
    moduleURL.searchParams.set("_cb_legacy_bundle", String(Date.now()));
    return import(moduleURL.href);
  })
  .catch(function () {
    reloadWithCacheBust("failed to load current UI entry");
  });
