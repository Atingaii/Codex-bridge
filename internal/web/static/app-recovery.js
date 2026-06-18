(function () {
  var recoveryKey = "codexBridge.uiRecoveryAttempts.v1";
  var legacyEntryRecoveryKey = "codexBridge.legacyEntryRecoveryAttempts.v1";
  var legacyEntryNames = [
    "index-BWIkJOjq.js",
    "index-BzGp0PoF.js",
    "index-C6X5HJo4.js",
    "index-D5GosAf8.js"
  ];
  var ready = false;
  var legacyRecoveryStarted = false;

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

  function isApplicationScript(target) {
    if (!target || target === window || target.tagName !== "SCRIPT" || !target.src) {
      return false;
    }
    try {
      var url = new URL(target.src, window.location.href);
      if (url.origin !== window.location.origin) return false;
      return url.pathname.indexOf("/assets/") === 0 && /\.js$/.test(url.pathname);
    } catch (_) {
      return false;
    }
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

  function renderFallback(reason, force) {
    var root = document.getElementById("root");
    if (!root || (rootHasContent() && !force)) return;
    if (force) {
      root.textContent = "";
    }
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

  function legacyEntryAttempts() {
    try {
      return Number(sessionStorage.getItem(legacyEntryRecoveryKey) || "0");
    } catch (_) {
      return 0;
    }
  }

  function setLegacyEntryAttempts(value) {
    try {
      sessionStorage.setItem(legacyEntryRecoveryKey, String(value));
    } catch (_) {
      // Session storage can be unavailable in restricted browsers.
    }
  }

  function clearLegacyEntryAttempts() {
    try {
      sessionStorage.removeItem(legacyEntryRecoveryKey);
    } catch (_) {
      // Session storage can be unavailable in restricted browsers.
    }
  }

  function isLegacyEntryScript(target) {
    if (!target || target.tagName !== "SCRIPT" || !target.src) return false;
    try {
      var url = new URL(target.src, window.location.href);
      if (url.origin !== window.location.origin) return false;
      return legacyEntryNames.some(function (name) {
        return url.pathname === "/assets/" + name || url.pathname.endsWith("/" + name);
      });
    } catch (_) {
      return false;
    }
  }

  function recoverLegacyEntry(script, reason) {
    if (legacyRecoveryStarted) return;
    legacyRecoveryStarted = true;
    try {
      if (script && script.parentNode) {
        script.type = "text/plain";
        script.parentNode.removeChild(script);
      }
    } catch (_) {
      // Removing a parser-inserted script is best effort; the reload below is authoritative.
    }
    if (legacyEntryAttempts() >= 2) {
      renderFallback(reason || "legacy application entry", true);
      return;
    }
    setLegacyEntryAttempts(legacyEntryAttempts() + 1);
    clearBrowserState().finally(function () {
      var url = new URL(window.location.href);
      url.searchParams.set("_cb_legacy_entry", String(Date.now()));
      window.location.replace(url.href);
    });
  }

  function checkLegacyEntryScripts() {
    Array.prototype.forEach.call(document.querySelectorAll("script[type='module'][src]"), function (script) {
      if (isLegacyEntryScript(script)) {
        recoverLegacyEntry(script, "legacy application entry " + script.getAttribute("src"));
      }
    });
  }

  if ("MutationObserver" in window) {
    var observer = new MutationObserver(function (records) {
      records.forEach(function (record) {
        Array.prototype.forEach.call(record.addedNodes || [], function (node) {
          if (isLegacyEntryScript(node)) {
            recoverLegacyEntry(node, "legacy application entry " + node.getAttribute("src"));
          }
        });
      });
    });
    observer.observe(document.documentElement || document, { childList: true, subtree: true });
  }

  window.setTimeout(checkLegacyEntryScripts, 0);
  document.addEventListener("DOMContentLoaded", checkLegacyEntryScripts);

  window.__codexBridgeAppReady = function () {
    ready = true;
    clearRecoveryAttempts();
    clearLegacyEntryAttempts();
  };

  window.addEventListener("error", function (event) {
    var target = event.target;
    if (!ready && !rootHasContent() && isApplicationScript(target)) {
      reloadWithCacheBust("script load failed");
      return;
    }
    if (target && target !== window) {
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
