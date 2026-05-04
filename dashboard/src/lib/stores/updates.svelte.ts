import { api, type UpdateStatus } from '../api';

// Shared updater state. Fetched once per session on demand; both the global
// banner and the Settings panel read from here so they stay in sync.
const SESSION_DISMISS_KEY = 'micelio.update.dismissedVersion';

class UpdateStore {
  status = $state<UpdateStatus | null>(null);
  loading = $state(false);
  installing = $state(false);
  error = $state<string | null>(null);
  dismissedVersion = $state<string | null>(null);

  constructor() {
    try {
      this.dismissedVersion = sessionStorage.getItem(SESSION_DISMISS_KEY);
    } catch {
      this.dismissedVersion = null;
    }
  }

  async load(force = false) {
    if (this.loading) return;
    if (!force && this.status) return;
    this.loading = true;
    this.error = null;
    try {
      this.status = force ? await api.forceUpdateCheck() : await api.getUpdateStatus();
    } catch (err) {
      this.error = (err as Error).message;
    } finally {
      this.loading = false;
    }
  }

  async install() {
    if (!this.status?.updateAvailable || this.installing) return null;
    this.installing = true;
    this.error = null;
    try {
      const res = await api.installUpdate();
      this.status = res.status;
      return res;
    } catch (err) {
      this.error = (err as Error).message;
      throw err;
    } finally {
      this.installing = false;
    }
  }

  async rollback() {
    if (!this.status?.canRollback) return null;
    this.installing = true;
    this.error = null;
    try {
      const res = await api.rollbackUpdate();
      // Refresh status after rollback
      await this.load(true);
      return res;
    } catch (err) {
      this.error = (err as Error).message;
      throw err;
    } finally {
      this.installing = false;
    }
  }

  dismiss() {
    if (!this.status?.latest) return;
    this.dismissedVersion = this.status.latest;
    try {
      sessionStorage.setItem(SESSION_DISMISS_KEY, this.status.latest);
    } catch { /* ignore */ }
  }

  get shouldShowBanner(): boolean {
    const s = this.status;
    if (!s) return false;
    if (!s.updateAvailable) return false;
    if (s.isDevBuild) return false;
    if (s.latest && s.latest === this.dismissedVersion) return false;
    return true;
  }
}

export const updates = new UpdateStore();
