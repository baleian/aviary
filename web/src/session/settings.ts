// What an installation sets for its console. The console reads it from
// /config.json at its own address before it shows anything.
export interface Settings {
  // The address of the API, if it is not the console's own: such as
  // https://api.example.com, which has to allow the origin of the console.
  api?: string;
  // The OpenID Connect issuer that signs callers in, and the client that
  // the console is to it. Without them the console signs nobody in.
  oidc?: Oidc;
}

export interface Oidc {
  issuer: string;
  clientId: string;
  // The scopes that the console asks the issuer for, separated by spaces.
  scopes?: string;
}

// readSettings returns the settings of the installation. An address that
// serves none sets nothing.
export async function readSettings(): Promise<Settings> {
  try {
    const response = await fetch("/config.json", { cache: "no-store" });
    const { api, oidc } = ((await response.json()) ?? {}) as Settings;
    const settings: Settings = {};
    if (api) settings.api = api.replace(/\/+$/, "");
    if (oidc?.issuer && oidc.clientId) settings.oidc = oidc;
    return settings;
  } catch {
    // No answer, or one that is not JSON, such as the page that a server
    // sends for every path it does not know.
    return {};
  }
}
