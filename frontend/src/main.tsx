
  import { useEffect } from "react";
  import { createRoot } from "react-dom/client";
  import App from "./app/App.tsx";
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

    return <App />;
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
