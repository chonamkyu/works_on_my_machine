import type { AuthProviderProps } from "react-oidc-context";

export const devMode = import.meta.env.VITE_AUTH_DEV === "1";

export const oidcConfig: AuthProviderProps = {
  authority: import.meta.env.VITE_KEYCLOAK_ISSUER ?? "",
  client_id: import.meta.env.VITE_KEYCLOAK_CLIENT_ID ?? "platform",
  redirect_uri: window.location.origin,
  post_logout_redirect_uri: window.location.origin,
  scope: "openid profile email",
  onSigninCallback: () =>
    window.history.replaceState({}, document.title, window.location.pathname),
};

// Module-level token + active-workspace holders so non-React api.ts can read
// them without importing React hooks.
let _token: string | undefined;
let _workspace: string | undefined;

export const setAuthToken = (t?: string) => { _token = t; };
export const getAuthToken = () => _token;
export const setActiveWorkspace = (w?: string) => { _workspace = w; };
export const getActiveWorkspace = () => _workspace;
