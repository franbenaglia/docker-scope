// Notificaciones del navegador: sólo si la persona las activa explícitamente
// (SPEC "Alertas"). Se avisa de incidentes abiertos no suprimidos por mantenimiento.
import type { Incident } from './api';

const KEY = 'dockerscope.notifications';

type Perm = NotificationPermission | 'unsupported';

class Notifier {
  supported = typeof window !== 'undefined' && 'Notification' in window;
  permission = $state<Perm>(this.supported ? Notification.permission : 'unsupported');
  enabled = $state(this.#load());

  #load(): boolean {
    try {
      return localStorage.getItem(KEY) === '1';
    } catch {
      return false;
    }
  }

  #save(v: boolean) {
    try {
      localStorage.setItem(KEY, v ? '1' : '0');
    } catch {
      // sin almacenamiento: la preferencia dura sólo esta sesión
    }
  }

  get active() {
    return this.enabled && this.permission === 'granted';
  }

  async enable() {
    if (!this.supported) return;
    this.permission = await Notification.requestPermission();
    this.enabled = this.permission === 'granted';
    this.#save(this.enabled);
  }

  disable() {
    this.enabled = false;
    this.#save(false);
  }

  incidentOpened(inc: Incident) {
    if (!this.active || inc.suppressed) return;
    try {
      const n = new Notification(`DockerScope: ${inc.container_name}`, {
        body: inc.summary,
        tag: `incident-${inc.id}`, // evita duplicados del mismo incidente
      });
      n.onclick = () => {
        window.focus();
        location.hash = '#/incidents';
      };
    } catch {
      // algunos navegadores sólo permiten notificaciones desde un service worker
    }
  }
}

export const notifier = new Notifier();
