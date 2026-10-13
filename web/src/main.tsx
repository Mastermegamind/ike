import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { AppearanceProvider } from "./theme/appearance";
import { ToastProvider } from "./components/ui";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AppearanceProvider>
      <ToastProvider>
        <App />
      </ToastProvider>
    </AppearanceProvider>
  </StrictMode>,
);
