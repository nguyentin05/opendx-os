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

export async function finishSignIn(): Promise<{ token: string; expires: number } | null> {
  const params = new URLSearchParams(window.location.search);
  const code = params.get('code');
  if (!code) return null;

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
    if (typeof saved.requestedRole === 'string') {
      returnURL += '?' + new URLSearchParams({ role: saved.requestedRole });
    }
  } finally {
    window.history.replaceState(null, '', returnURL);
    window.dispatchEvent(new PopStateEvent('popstate'));
  }

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
  const token = await response.json() as { access_token: string; token_type: string; expires_in: number };
  if (!token.access_token || token.token_type.toLowerCase() !== 'bearer' || token.expires_in < 1) {
    throw new Error('Sign-in failed.');
  }

  return { token: token.access_token, expires: Date.now() + token.expires_in * 1000 };
}
export async function loadSession(token:string,signal:AbortSignal):Promise<Session>{const headers={Authorization:'Bearer '+token};const userResponse=await fetch('/api/auth/me',{headers,signal});if(!userResponse.ok)throw new Error('Sign-in or current permissions are unavailable.');const user=await userResponse.json() as CurrentUser;const appsResponse=await fetch('/api/auth/apps',{headers,signal});if(!appsResponse.ok)throw new Error('Applications are unavailable.');const {apps}=await appsResponse.json() as {apps:InstalledApp[]};if(signal.aborted)throw new DOMException('Context changed','AbortError');return {token,user,apps}}
