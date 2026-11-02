// Device profiles a crawl can pretend to be. Mirrors types.DeviceProfiles in
// internal/types/devices.go (TestDashboardDevicesMatch keeps them in sync).

export interface DeviceProfile {
  id: string;
  label: string;
  kind: 'desktop' | 'mobile' | 'tablet';
  width: number;
  height: number;
  scale: number;
  userAgent: string;
}

/** What a crawl config stores: a built-in id (+ landscape) or a custom screen. */
export interface DeviceChoice {
  id: string;
  landscape?: boolean;
  width?: number;
  height?: number;
  scale?: number;
  mobile?: boolean;
  touch?: boolean;
}

const CHROME = '136.0.0.0';

export const DEVICES: DeviceProfile[] = [
  { id: 'desktop', label: 'Desktop', kind: 'desktop', width: 1920, height: 1080, scale: 1,
    userAgent: `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${CHROME} Safari/537.36` },
  { id: 'laptop', label: 'Laptop', kind: 'desktop', width: 1366, height: 768, scale: 1,
    userAgent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${CHROME} Safari/537.36` },
  { id: 'tablet', label: 'Tablet', kind: 'tablet', width: 820, height: 1180, scale: 2,
    userAgent: 'Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1' },
  { id: 'mobile', label: 'Mobile', kind: 'mobile', width: 412, height: 915, scale: 2.625,
    userAgent: `Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${CHROME} Mobile Safari/537.36` },
  { id: 'iphone', label: 'iPhone', kind: 'mobile', width: 390, height: 844, scale: 3,
    userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1' },
  { id: 'googlebot-smartphone', label: 'Googlebot Smartphone', kind: 'mobile', width: 412, height: 732, scale: 2.625,
    userAgent: `Mozilla/5.0 (Linux; Android 6.0.1; Nexus 5X Build/MMB29P) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${CHROME} Mobile Safari/537.36 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)` },
  { id: 'googlebot-desktop', label: 'Googlebot Desktop', kind: 'desktop', width: 1024, height: 768, scale: 1,
    userAgent: `Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Googlebot/2.1; +http://www.google.com/bot.html) Chrome/${CHROME} Safari/537.36` },
  { id: 'bingbot', label: 'Bingbot', kind: 'desktop', width: 1366, height: 768, scale: 1,
    userAgent: `Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm) Chrome/${CHROME} Safari/537.36` },
];

/** Screen the crawl renders on: phones and tablets are portrait unless landscape. */
export function deviceViewport(c: DeviceChoice): { width: number; height: number } {
  const base = DEVICES.find((d) => d.id === c.id);
  const w = c.width ?? base?.width ?? 1920;
  const h = c.height ?? base?.height ?? 1080;
  const kind = base?.kind ?? (c.mobile ? 'mobile' : 'desktop');
  if (kind === 'desktop') return { width: w, height: h };
  return c.landscape ? { width: Math.max(w, h), height: Math.min(w, h) } : { width: Math.min(w, h), height: Math.max(w, h) };
}

/** Short label like "Mobile 412×915" for badges; "" for no device. */
export function deviceSummary(c: DeviceChoice | null | undefined): string {
  if (!c?.id) return '';
  const base = DEVICES.find((d) => d.id === c.id);
  const { width, height } = deviceViewport(c);
  return `${base?.label ?? 'Custom'} ${width}×${height}${c.landscape && base?.kind !== 'desktop' ? ' landscape' : ''}`;
}

export function deviceKind(c: DeviceChoice | null | undefined): 'desktop' | 'mobile' | 'tablet' | null {
  if (!c?.id) return null;
  return DEVICES.find((d) => d.id === c.id)?.kind ?? (c.mobile ? 'mobile' : 'desktop');
}
