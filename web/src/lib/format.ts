// Formato de valores para la interfaz. Las fechas llegan en UTC y se muestran en hora local.

export function fmtTime(iso: string): string {
  return new Date(iso).toLocaleTimeString();
}

/** Duración compacta: 45s, 12m, 3h 5m, 2d 4h. */
export function fmtDuration(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 ? `${h}h ${m % 60}m` : `${h}h`;
  const d = Math.floor(h / 24);
  return h % 24 ? `${d}d ${h % 24}h` : `${d}d`;
}

const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];

/** Bytes en unidades binarias (1 KiB = 1024 B). */
export function fmtBytes(n: number): string {
  let v = n;
  let i = 0;
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 100 || i === 0 ? v.toFixed(0) : v.toFixed(1)} ${UNITS[i]}`;
}

export function fmtRate(bps: number): string {
  return `${fmtBytes(bps)}/s`;
}

export function fmtPercent(p: number): string {
  return `${p >= 10 ? p.toFixed(0) : p.toFixed(1)}%`;
}

export function fmtCores(c: number): string {
  return c.toLocaleString(undefined, { maximumFractionDigits: 2 });
}
