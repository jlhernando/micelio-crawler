/**
 * Google Search Console OAuth2 authentication.
 *
 * Supports two auth modes:
 * 1. OAuth2 interactive flow (for personal use) — `micelio gsc-auth`
 * 2. Service account key file (for CI/server use) — `--gsc-key-file key.json`
 */

import { google } from 'googleapis';
import { readFileSync, writeFileSync, mkdirSync, existsSync, unlinkSync } from 'node:fs';
import { join } from 'node:path';
import { homedir } from 'node:os';
import { createServer } from 'node:http';
import { execFile } from 'node:child_process';

// Micelio's OAuth2 client credentials (public — safe to embed in CLI tools)
// Users must still consent via Google's OAuth screen.
const CLIENT_ID = ''; // Will be set via env or config
const CLIENT_SECRET = '';
const REDIRECT_URI = 'http://localhost:9549/oauth2callback';
const SCOPES = ['https://www.googleapis.com/auth/webmasters.readonly'];

const CONFIG_DIR = join(homedir(), '.micelio');
const TOKEN_PATH = join(CONFIG_DIR, 'gsc-token.json');

interface StoredToken {
  access_token: string;
  refresh_token: string;
  scope: string;
  token_type: string;
  expiry_date: number;
}

/**
 * Get the path to the config directory.
 */
export function getConfigDir(): string {
  return CONFIG_DIR;
}

/**
 * Check if a GSC token file exists.
 */
export function hasStoredToken(): boolean {
  return existsSync(TOKEN_PATH);
}

/**
 * Create an authenticated OAuth2 client from stored token.
 * Returns null if no token is stored or credentials are missing.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function getAuthClient(clientId?: string, clientSecret?: string): any | null {
  const cid = clientId || process.env.MICELIO_GSC_CLIENT_ID || CLIENT_ID;
  const csec = clientSecret || process.env.MICELIO_GSC_CLIENT_SECRET || CLIENT_SECRET;

  if (!cid || !csec) {
    return null;
  }

  const oauth2Client = new google.auth.OAuth2(cid, csec, REDIRECT_URI);

  if (!existsSync(TOKEN_PATH)) {
    return null;
  }

  try {
    const tokenData = JSON.parse(readFileSync(TOKEN_PATH, 'utf-8')) as StoredToken;
    oauth2Client.setCredentials(tokenData);
    return oauth2Client;
  } catch {
    return null;
  }
}

/**
 * Create an authenticated client from a service account key file.
 */
export function getServiceAccountClient(keyFilePath: string) {
  const auth = new google.auth.GoogleAuth({
    keyFile: keyFilePath,
    scopes: SCOPES,
  });
  return auth;
}

/**
 * Run the interactive OAuth2 flow. Opens a local server, prints the auth URL,
 * waits for the callback, and stores the token.
 */
export async function runOAuthFlow(clientId?: string, clientSecret?: string): Promise<void> {
  const cid = clientId || process.env.MICELIO_GSC_CLIENT_ID || CLIENT_ID;
  const csec = clientSecret || process.env.MICELIO_GSC_CLIENT_SECRET || CLIENT_SECRET;

  if (!cid || !csec) {
    console.error(
      'Error: GSC OAuth2 credentials not configured.\n\n' +
      'Set environment variables:\n' +
      '  MICELIO_GSC_CLIENT_ID=your-client-id\n' +
      '  MICELIO_GSC_CLIENT_SECRET=your-client-secret\n\n' +
      'Or pass them as options:\n' +
      '  micelio gsc-auth --client-id <id> --client-secret <secret>\n\n' +
      'To get credentials:\n' +
      '  1. Go to https://console.cloud.google.com/apis/credentials\n' +
      '  2. Create an OAuth 2.0 Client ID (Desktop or Web application)\n' +
      '  3. Add http://localhost:9549/oauth2callback as a redirect URI\n' +
      '  4. Enable the Search Console API for your project'
    );
    process.exit(1);
  }

  const oauth2Client = new google.auth.OAuth2(cid, csec, REDIRECT_URI);

  const authUrl = oauth2Client.generateAuthUrl({
    access_type: 'offline',
    scope: SCOPES,
    prompt: 'consent',
  });

  console.log('\nOpen this URL in your browser to authorize Micelio:\n');
  console.log(`  ${authUrl}\n`);
  console.log('Waiting for authorization...\n');

  // Start local server to receive the OAuth callback
  const code = await new Promise<string>((resolve, reject) => {
    const server = createServer((req, res) => {
      const url = new URL(req.url || '', `http://localhost:9549`);
      const authCode = url.searchParams.get('code');
      const error = url.searchParams.get('error');

      if (error) {
        res.writeHead(200, { 'Content-Type': 'text/html' });
        res.end('<h1>Authorization failed</h1><p>You can close this window.</p>');
        server.close();
        reject(new Error(`OAuth error: ${error}`));
        return;
      }

      if (authCode) {
        res.writeHead(200, { 'Content-Type': 'text/html' });
        res.end('<h1>Authorization successful!</h1><p>You can close this window and return to the terminal.</p>');
        server.close();
        resolve(authCode);
        return;
      }

      // Default response for non-callback paths (favicon.ico, etc.)
      res.writeHead(404);
      res.end();
    });

    server.listen(9549, () => {
      // Try to open the URL automatically
      const platform = process.platform;
      if (platform === 'darwin') execFile('open', [authUrl]);
      else if (platform === 'linux') execFile('xdg-open', [authUrl]);
      else if (platform === 'win32') execFile('start', [authUrl]);
    });

    // Timeout after 5 minutes
    setTimeout(() => {
      server.close();
      reject(new Error('OAuth flow timed out after 5 minutes'));
    }, 300_000);
  });

  // Exchange the code for tokens
  const { tokens } = await oauth2Client.getToken(code);

  // Store the token with restrictive permissions (0o600 = owner read/write only)
  mkdirSync(CONFIG_DIR, { recursive: true });
  writeFileSync(TOKEN_PATH, JSON.stringify(tokens, null, 2), { encoding: 'utf-8', mode: 0o600 });

  console.log('Authorization successful! Token saved to ~/.micelio/gsc-token.json\n');

  // List available properties
  oauth2Client.setCredentials(tokens);
  const webmasters = google.searchconsole({ version: 'v1', auth: oauth2Client });
  try {
    const sites = await webmasters.sites.list();
    const siteEntries = sites.data.siteEntry || [];
    if (siteEntries.length > 0) {
      console.log('Available Search Console properties:\n');
      for (const site of siteEntries) {
        console.log(`  ${site.siteUrl}  (${site.permissionLevel})`);
      }
      console.log('\nUse with: micelio spider <url> --gsc --gsc-property <property-url>\n');
    }
  } catch {
    // Non-fatal — token works, just can't list properties
  }
}

/**
 * Delete the locally stored GSC token file.
 * Note: This only removes the local copy. The refresh token remains valid at Google
 * until it expires or the user revokes it at https://myaccount.google.com/permissions.
 */
export function deleteStoredToken(): void {
  if (existsSync(TOKEN_PATH)) {
    unlinkSync(TOKEN_PATH);
    console.log('Token deleted: ~/.micelio/gsc-token.json');
    console.log('To fully revoke access, visit: https://myaccount.google.com/permissions');
  } else {
    console.log('No stored token found.');
  }
}

/** @deprecated Use deleteStoredToken() instead */
export const revokeToken = deleteStoredToken;
