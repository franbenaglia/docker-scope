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
