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

function reloadWithCacheBust() {
  var attempts = legacyAttempts();
  if (attempts >= 1) return;
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
      reloadWithCacheBust();
      return;
    }
    currentStylesheetSources(html).forEach(loadStylesheet);
    var moduleURL = new URL(src, window.location.origin);
    moduleURL.searchParams.set("_cb_legacy_bundle", String(Date.now()));
    return import(moduleURL.href);
  })
  .catch(function () {
    reloadWithCacheBust();
  });
