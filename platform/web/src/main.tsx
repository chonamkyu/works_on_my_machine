import React from "react";
import ReactDOM from "react-dom/client";
import { AuthProvider } from "react-oidc-context";
import { App, AuthGate } from "./App";
import { devMode, oidcConfig } from "./lib/auth";
import "./index.css";

const tree = devMode ? (
  <App />
) : (
  <AuthProvider {...oidcConfig}>
    <AuthGate>
      <App />
    </AuthGate>
  </AuthProvider>
);

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    {tree}
  </React.StrictMode>,
);
