import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { AppearanceProvider } from "./theme/appearance";
import { ToastProvider } from "./components/ui";
import { I18nProvider } from "./i18n";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <I18nProvider>
      <AppearanceProvider>
        <ToastProvider>
          <App />
        </ToastProvider>
      </AppearanceProvider>
    </I18nProvider>
  </StrictMode>,
);
