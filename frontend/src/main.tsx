
  import { useEffect } from "react";
  import { createRoot } from "react-dom/client";
  import App from "./app/App.tsx";
  import { AppErrorBoundary } from "./app/components/AppErrorBoundary.tsx";
  import "./styles/index.css";

  declare global {
    interface Window {
      __codexBridgeAppReady?: () => void;
    }
  }

  function Root() {
    useEffect(() => {
      window.__codexBridgeAppReady?.();
    }, []);

    return <AppErrorBoundary><App /></AppErrorBoundary>;
  }

  createRoot(document.getElementById("root")!).render(<Root />);

  if ("serviceWorker" in navigator) {
    window.addEventListener("load", () => {
      navigator.serviceWorker
        .register("/sw.js", { updateViaCache: "none" })
        .then((registration) => registration.update().catch(() => undefined))
        .catch(() => undefined);
    });
  }
