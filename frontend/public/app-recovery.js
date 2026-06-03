(function () {
  var recoveryKey = "codexBridge.uiRecoveryAttempts.v1";
  var ready = false;

  function rootHasContent() {
    var root = document.getElementById("root");
    return Boolean(root && root.childElementCount > 0);
  }

  function recoveryAttempts() {
    try {
      return Number(sessionStorage.getItem(recoveryKey) || "0");
    } catch (_) {
      return 0;
    }
  }

  function setRecoveryAttempts(value) {
    try {
      sessionStorage.setItem(recoveryKey, String(value));
    } catch (_) {
      // Session storage can be unavailable in restricted browsers.
    }
  }

  function clearRecoveryAttempts() {
    try {
      sessionStorage.removeItem(recoveryKey);
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

  function reloadWithCacheBust(reason) {
    var attempts = recoveryAttempts();
    if (attempts >= 1) {
      renderFallback(reason);
      return;
    }
    setRecoveryAttempts(attempts + 1);
    clearBrowserState().finally(function () {
      var url = new URL(window.location.href);
      url.searchParams.set("_cb_ui_reload", String(Date.now()));
      window.location.replace(url.href);
    });
  }

  function renderFallback(reason) {
    var root = document.getElementById("root");
    if (!root || rootHasContent()) return;
    var message = document.createElement("div");
    message.style.cssText = "box-sizing:border-box;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px;font:14px/1.5 system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;color:#111827;background:#f9fafb;";
    message.innerHTML = "<div style=\"max-width:520px\"><h1 style=\"margin:0 0 8px;font-size:18px\">Codex Bridge UI needs a refresh</h1><p style=\"margin:0;color:#4b5563\">The browser still has an old application bundle cached. Reload this page once more after the Bridge service has been updated. Reason: " + escapeHTML(reason || "startup") + ".</p></div>";
    root.appendChild(message);
  }

  function escapeHTML(value) {
    return String(value).replace(/[&<>"']/g, function (char) {
      return {
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&#39;"
      }[char];
    });
  }

  window.__codexBridgeAppReady = function () {
    ready = true;
    clearRecoveryAttempts();
  };

  window.addEventListener("error", function (event) {
    var target = event.target;
    if (target && target !== window && target.tagName === "SCRIPT") {
      reloadWithCacheBust("script load failed");
      return;
    }
    if (!ready && !rootHasContent()) {
      reloadWithCacheBust(event.message || "startup error");
    }
  }, true);

  window.addEventListener("unhandledrejection", function (event) {
    if (!ready && !rootHasContent()) {
      var reason = event.reason && (event.reason.message || event.reason);
      reloadWithCacheBust(reason || "startup promise rejected");
    }
  });

  window.setTimeout(function () {
    if (!ready && !rootHasContent()) {
      reloadWithCacheBust("startup timeout");
    }
  }, 5000);
})();
