// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
import type { CurrentUser, InstalledApp } from './types/registry';
import { parseRoute } from './core/routes';
export interface Session { token:string; user:CurrentUser; apps:InstalledApp[] }
interface Config { issuer:string; clientID:string }
declare global { interface Window { DXOS_CONFIG?:Config } }
const config=():Config=>{if(window.DXOS_CONFIG)return window.DXOS_CONFIG;if(['localhost','127.0.0.1'].includes(window.location.hostname))return {issuer:'http://localhost:8085/realms/dxos',clientID:'dxos-portal'};throw new Error('Sign-in configuration is unavailable.');};
const random=()=>Array.from(crypto.getRandomValues(new Uint8Array(32)),b=>b.toString(16).padStart(2,'0')).join('');
const redirectURI=()=>window.location.origin+window.location.pathname;
async function discovery(){const c=config();const issuer=new URL(c.issuer);if(issuer.protocol!=='https:'&&!['localhost','127.0.0.1'].includes(issuer.hostname))throw new Error('Secure sign-in configuration is unavailable.');const r=await fetch(c.issuer.replace(/\/$/,'')+'/.well-known/openid-configuration');if(!r.ok)throw new Error('Sign-in is unavailable.');const data=await r.json() as {issuer:string;authorization_endpoint:string;token_endpoint:string};if(data.issuer.replace(/\/$/,'')!==c.issuer.replace(/\/$/,''))throw new Error('Invalid sign-in configuration.');for(const endpoint of [data.authorization_endpoint,data.token_endpoint])if(new URL(endpoint).origin!==issuer.origin)throw new Error('Invalid sign-in endpoint.');return {config:c,...data}}
interface SignInState {
  verifier: string;
  state: string;
  created: number;
  redirectURI?: string;
  requestedRole?: string;
}

export async function signIn() {
  const redirect = redirectURI();
  const route = parseRoute(window.location.pathname, window.location.search);
  const { config, authorization_endpoint } = await discovery();
  const verifier = random();
  const state = random();
  const saved: SignInState = {
    verifier,
    state,
    created: Date.now(),
    redirectURI: redirect,
    requestedRole: route.kind === 'app' ? route.requestedRole : undefined,
  };
  sessionStorage.setItem('dxos.pkce', JSON.stringify(saved));

  const hash = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier)));
  const challenge = btoa(String.fromCharCode(...hash)).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
  const url = new URL(authorization_endpoint);
  url.search = new URLSearchParams({
    client_id: config.clientID,
    redirect_uri: redirect,
    response_type: 'code',
    scope: 'openid profile email',
    state,
    code_challenge: challenge,
    code_challenge_method: 'S256',
  }).toString();
  window.location.assign(url.toString());
}

export interface SignInTokens {
  token: string;
  expires: number;
  refreshToken?: string;
  refreshExpires?: number;
}

export async function finishSignIn(): Promise<SignInTokens | null> {
  const params = new URLSearchParams(window.location.search);
  const code = params.get('code');
  const providerError = params.has('error');
  if (!code && !providerError) return null;

  const redirect = redirectURI();
  const raw = sessionStorage.getItem('dxos.pkce');
  sessionStorage.removeItem('dxos.pkce');
  let saved: SignInState;
  let returnURL = redirect;
  try {
    if (!raw) throw new Error('Sign-in state expired.');
    saved = JSON.parse(raw) as SignInState;
    if (params.get('state') !== saved.state ||
        Date.now() - saved.created > 300000 ||
        (saved.redirectURI !== undefined && saved.redirectURI !== redirect)) {
      throw new Error('Sign-in state expired.');
    }
    if (saved.redirectURI === redirect && typeof saved.requestedRole === 'string') {
      returnURL += '?' + new URLSearchParams({ role: saved.requestedRole });
    }
  } finally {
    window.history.replaceState(null, '', returnURL);
    window.dispatchEvent(new PopStateEvent('popstate'));
  }

  if (providerError || !code) throw new Error('Sign-in failed.');

  const { config, token_endpoint } = await discovery();
  const response = await fetch(token_endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'authorization_code',
      code,
      client_id: config.clientID,
      redirect_uri: redirect,
      code_verifier: saved.verifier,
    }),
  });
  if (!response.ok) throw new Error('Sign-in failed.');
  const token = await response.json() as {
    access_token: string; token_type: string; expires_in: number;
    refresh_token?: string; refresh_expires_in?: number;
  };
  if (!token.access_token || token.token_type.toLowerCase() !== 'bearer' || token.expires_in < 1) {
    throw new Error('Sign-in failed.');
  }

  const now = Date.now();
  return {
    token: token.access_token,
    expires: now + token.expires_in * 1000,
    refreshToken: typeof token.refresh_token === 'string' && token.refresh_token ? token.refresh_token : undefined,
    refreshExpires: typeof token.refresh_expires_in === 'number' && token.refresh_expires_in >= 1
      ? now + token.refresh_expires_in * 1000
      : undefined,
  };
}

export type RefreshOutcome = 'ok' | 'unavailable' | 'ended';

interface TokenState {
  access: string;
  accessDeadline: number;
  refresh?: string;
  refreshDeadline?: number;
  inflight?: Promise<RefreshOutcome>;
}

// The sign-in token stays the identity; only this holder changes on refresh.
const tokens = new Map<string, TokenState>();
const watchers = new Map<string, () => void>();

export function registerSession(session: SignInTokens): void {
  tokens.set(session.token, {
    access: session.token,
    accessDeadline: session.expires,
    refresh: session.refreshToken,
    refreshDeadline: session.refreshExpires,
  });
}

// Identity stays stable and the Authorization header is live.
export function bearer(token: string): string {
  return 'Bearer ' + (tokens.get(token)?.access ?? token);
}

export function endSession(token: string): void {
  tokens.delete(token);
  const stop = watchers.get(token);
  watchers.delete(token);
  stop?.();
}

export function sessionDeadlines(token: string): { access: number; refresh?: number } | undefined {
  const state = tokens.get(token);
  return state ? { access: state.accessDeadline, refresh: state.refreshDeadline } : undefined;
}

export async function refreshSession(token: string): Promise<RefreshOutcome> {
  const state = tokens.get(token);
  if (!state?.refresh) return 'ended';
  if (state.inflight) return state.inflight;

  const run = (async (): Promise<RefreshOutcome> => {
    try {
      const { config, token_endpoint } = await discovery();
      const response = await fetch(token_endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams({
          grant_type: 'refresh_token',
          client_id: config.clientID,
          refresh_token: state.refresh!,
        }),
      });
      // Sign-out or a replaced session must not be revived by a late response.
      if (tokens.get(token) !== state) return 'ended';
      if (!response.ok) return response.status >= 500 ? 'unavailable' : 'ended';

      const body = await response.json() as {
        access_token: string; token_type: string; expires_in: number;
        refresh_token?: string; refresh_expires_in?: number;
      };
      if (tokens.get(token) !== state) return 'ended';
      if (!body.access_token || typeof body.token_type !== 'string'
        || body.token_type.toLowerCase() !== 'bearer' || !(body.expires_in >= 1)) {
        return 'ended';
      }

      const now = Date.now();
      state.access = body.access_token;
      state.accessDeadline = now + body.expires_in * 1000;
      if (typeof body.refresh_token === 'string' && body.refresh_token) state.refresh = body.refresh_token;
      if (typeof body.refresh_expires_in === 'number' && body.refresh_expires_in >= 1) {
        state.refreshDeadline = now + body.refresh_expires_in * 1000;
      }
      return 'ok';
    } catch {
      return 'unavailable';
    }
  })();

  state.inflight = run;
  try {
    return await run;
  } finally {
    if (state.inflight === run) state.inflight = undefined;
  }
}

const retryDelays = [5000, 15000, 30000];

function refreshDelay(now: number, deadlines: { access: number; refresh?: number }): number {
  const window = Math.min(deadlines.access, deadlines.refresh ?? Infinity) - now;
  if (!Number.isFinite(window)) return 5000;
  // Under four minutes, refresh at 75% of the window; otherwise keep a 60 s margin.
  return window < 240000 ? Math.max(5000, window * 0.75) : Math.max(5000, window - 60000);
}

export function watchSession(token: string, onEnded: () => void): () => void {
  const state = tokens.get(token);
  if (!state) return () => {};

  watchers.get(token)?.();
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let retry = 0;

  const stop = () => {
    if (stopped) return;
    stopped = true;
    if (timer !== undefined) clearTimeout(timer);
    document.removeEventListener('visibilitychange', wake);
    window.removeEventListener('online', wake);
    if (watchers.get(token) === stop) watchers.delete(token);
  };

  const finish = () => {
    if (stopped) return;
    stop();
    tokens.delete(token);
    onEnded();
  };

  const schedule = (delay: number) => {
    if (timer !== undefined) clearTimeout(timer);
    timer = setTimeout(() => void attempt(), Math.max(0, delay));
  };

  const scheduleNext = () => {
    const deadlines = sessionDeadlines(token);
    if (!deadlines) return finish();
    schedule(refreshDelay(Date.now(), deadlines));
  };

  const attempt = async () => {
    if (stopped) return;
    const deadlines = sessionDeadlines(token);
    if (!deadlines) return finish();

    const outcome = await refreshSession(token);
    if (stopped) return;
    if (outcome === 'ok') {
      retry = 0;
      scheduleNext();
      return;
    }
    if (outcome === 'ended') return finish();

    const current = sessionDeadlines(token);
    if (!current || Date.now() >= current.access) return finish();
    schedule(retryDelays[Math.min(retry++, retryDelays.length - 1)]);
  };

  function wake() {
    if (stopped || document.visibilityState === 'hidden') return;
    const deadlines = sessionDeadlines(token);
    if (!deadlines) return finish();
    if (Date.now() >= Math.min(deadlines.access, deadlines.refresh ?? Infinity)) void attempt();
  }

  document.addEventListener('visibilitychange', wake);
  window.addEventListener('online', wake);
  watchers.set(token, stop);
  // Without a refresh token the session ends exactly at access expiry.
  if (!state.refresh) schedule(Math.max(0, state.accessDeadline - Date.now()));
  else scheduleNext();
  return stop;
}

export async function loadSession(token:string,signal:AbortSignal):Promise<Session>{const headers={Authorization:bearer(token)};const userResponse=await fetch('/api/auth/me',{headers,signal});if(!userResponse.ok)throw new Error('Sign-in or current permissions are unavailable.');const user=await userResponse.json() as CurrentUser;const appsResponse=await fetch('/api/auth/apps',{headers,signal});if(!appsResponse.ok)throw new Error('Applications are unavailable.');const {apps}=await appsResponse.json() as {apps:InstalledApp[]};if(signal.aborted)throw new DOMException('Context changed','AbortError');return {token,user,apps}}
